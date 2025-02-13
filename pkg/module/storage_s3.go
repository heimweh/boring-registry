package module

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/pkg/errors"
)

// S3Storage is a Storage implementation backed by S3.
type S3Storage struct {
	s3             *s3.S3
	uploader       *s3manager.Uploader
	bucket         string
	bucketPrefix   string
	archiveFormat  string
	bucketRegion   string
	pathStyle      bool
	bucketEndpoint string
}

// GetModule retrieves information about a module from the S3 storage.
func (s *S3Storage) GetModule(ctx context.Context, namespace, name, provider, version string) (Module, error) {
	key := storagePath(s.bucketPrefix, namespace, name, provider, version, s.archiveFormat)

	input := &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}

	if _, err := s.s3.HeadObject(input); err != nil {
		return Module{}, errors.Wrap(ErrNotFound, err.Error())
	}

	return Module{
		Namespace:   namespace,
		Name:        name,
		Provider:    provider,
		Version:     version,
		DownloadURL: fmt.Sprintf("%s.s3-%s.amazonaws.com/%s", s.bucket, s.bucketRegion, *input.Key),
	}, nil
}

func (s *S3Storage) ListModuleVersions(ctx context.Context, namespace, name, provider string) ([]Module, error) {
	var modules []Module

	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(storagePrefix(s.bucketPrefix, namespace, name, provider)),
	}

	fn := func(page *s3.ListObjectsV2Output, last bool) bool {
		for _, obj := range page.Contents {
			metadata := objectMetadata(*obj.Key)

			version, ok := metadata["version"]
			if !ok {
				continue
			}

			module := Module{
				Namespace:   namespace,
				Name:        name,
				Provider:    provider,
				Version:     version,
				DownloadURL: fmt.Sprintf("%s.s3-%s.amazonaws.com/%s", s.bucket, s.bucketRegion, *obj.Key),
			}

			modules = append(modules, module)
		}

		return true
	}

	if err := s.s3.ListObjectsV2Pages(input, fn); err != nil {
		return nil, errors.Wrap(ErrListFailed, err.Error())
	}

	return modules, nil
}

// UploadModule uploads a module to the S3 storage.
func (s *S3Storage) UploadModule(ctx context.Context, namespace, name, provider, version string, body io.Reader) (Module, error) {
	if namespace == "" {
		return Module{}, errors.New("namespace not defined")
	}

	if name == "" {
		return Module{}, errors.New("name not defined")
	}

	if provider == "" {
		return Module{}, errors.New("provider not defined")
	}

	if version == "" {
		return Module{}, errors.New("version not defined")
	}

	key := storagePath(s.bucketPrefix, namespace, name, provider, version, DefaultArchiveFormat)

	if _, err := s.GetModule(ctx, namespace, name, provider, version); err == nil {
		return Module{}, errors.Wrap(ErrAlreadyExists, key)
	}

	input := &s3manager.UploadInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(storagePath(s.bucketPrefix, namespace, name, provider, version, DefaultArchiveFormat)),
		Body:   body,
	}

	if _, err := s.uploader.Upload(input); err != nil {
		return Module{}, errors.Wrapf(ErrUploadFailed, err.Error())
	}

	return s.GetModule(ctx, namespace, name, provider, version)
}

func (s *S3Storage) determineBucketRegion() (string, error) {
	region, err := s3manager.GetBucketRegionWithClient(context.Background(), s.s3, s.bucket)
	if err != nil {
		return "", err
	}

	return region, nil
}

// S3StorageOption provides additional options for the S3Storage.
type S3StorageOption func(*S3Storage)

// WithS3StorageBucketPrefix configures the s3 storage to work under a given prefix.
func WithS3StorageBucketPrefix(prefix string) S3StorageOption {
	return func(s *S3Storage) {
		s.bucketPrefix = prefix
	}
}

// WithS3ArchiveFormat configures the module archive format (zip, tar, tgz, etc.)
func WithS3ArchiveFormat(archiveFormat string) S3StorageOption {
	return func(s *S3Storage) {
		s.archiveFormat = archiveFormat
	}
}

// WithS3StorageBucketRegion configures the region for a given s3 storage.
func WithS3StorageBucketRegion(region string) S3StorageOption {
	return func(s *S3Storage) {
		s.bucketRegion = region
	}
}

// WithS3StorageBucketEndpoint configures the endpoint for a given s3 storage. (needed for MINIO)
func WithS3StorageBucketEndpoint(endpoint string) S3StorageOption {
	return func(s *S3Storage) {
		// default value is "", so don't set and leave to aws sdk
		if len(endpoint) > 0 {
			s.s3.Client.Endpoint = endpoint
		}
		s.bucketEndpoint = "aws sdk default"
	}
}

// WithS3StoragePathStyle configures if Path Style is used for a given s3 storage. (needed for MINIO)
func WithS3StoragePathStyle(pathStyle bool) S3StorageOption {
	return func(s *S3Storage) {
		// only set if true, default value is false but leave for aws sdk
		if pathStyle {
			s.s3.Client.Config.S3ForcePathStyle = &pathStyle
		}
		s.pathStyle = pathStyle
	}
}

// NewS3Storage returns a fully initialized S3 storage.
func NewS3Storage(bucket string, options ...S3StorageOption) (Storage, error) {
	sess, err := session.NewSession()
	if err != nil {
		return nil, err
	}

	s := &S3Storage{
		s3:            s3.New(sess),
		uploader:      s3manager.NewUploader(sess),
		bucket:        bucket,
		archiveFormat: DefaultArchiveFormat,
	}

	for _, option := range options {
		option(s)
	}

	if s.bucketRegion == "" {
		region, err := s.determineBucketRegion()
		if err != nil {
			return nil, errors.Wrap(err, "failed to determine bucket region")
		}
		s.bucketRegion = region
	}

	return s, nil
}
