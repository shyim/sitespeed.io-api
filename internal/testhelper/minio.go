package testhelper

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/shyim/sitespeed-api/internal/storage"
	"github.com/testcontainers/testcontainers-go/modules/minio"
)

const (
	MinioAccessKey = "minioadmin"
	MinioSecretKey = "minioadmin"
	BucketName     = "test-bucket"
)

// StartMinio starts a MinIO container and returns a storage config and cleanup func.
func StartMinio(t *testing.T, ctx context.Context) storage.Config {
	t.Helper()

	container, err := minio.Run(ctx,
		"minio/minio:latest",
		minio.WithUsername(MinioAccessKey),
		minio.WithPassword(MinioSecretKey),
	)
	if err != nil {
		t.Fatalf("failed to start minio container: %v", err)
	}
	t.Cleanup(func() {
		container.Terminate(context.Background())
	})

	endpoint, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get minio connection string: %v", err)
	}

	serviceURL := "http://" + endpoint

	// Create the test bucket
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
			return aws.Credentials{
				AccessKeyID:     MinioAccessKey,
				SecretAccessKey: MinioSecretKey,
			}, nil
		})),
		config.WithRegion("us-east-1"),
		config.WithEndpointResolverWithOptions(aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			return aws.Endpoint{
				URL:           serviceURL,
				SigningRegion: "us-east-1",
			}, nil
		})),
	)
	if err != nil {
		t.Fatalf("failed to load aws config: %v", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = true
	})

	_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(BucketName),
	})
	if err != nil {
		t.Fatalf("failed to create test bucket: %v", err)
	}

	return storage.Config{
		ServiceURL:            serviceURL,
		AccessKey:             MinioAccessKey,
		SecretKey:             MinioSecretKey,
		BucketName:            BucketName,
		DisablePayloadSigning: true,
	}
}
