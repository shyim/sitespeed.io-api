package storage

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Service struct {
	client                *s3.Client
	bucketName            string
	disablePayloadSigning bool
}

func NewService(ctx context.Context) (*Service, error) {
	serviceURL := os.Getenv("S3_SERVICE_URL")
	accessKey := os.Getenv("S3_ACCESS_KEY")
	secretKey := os.Getenv("S3_SECRET_KEY")
	bucketName := os.Getenv("S3_BUCKET_NAME")
	if bucketName == "" {
		bucketName = "sitespeed-results"
	}
	disablePayloadSigning := os.Getenv("S3_DISABLE_PAYLOAD_SIGNING") != "false"

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
			return aws.Credentials{
				AccessKeyID:     accessKey,
				SecretAccessKey: secretKey,
			}, nil
		})),
		config.WithRegion("us-east-1"), // Region is usually required but often ignored with custom endpoints
		config.WithEndpointResolverWithOptions(aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			if serviceURL != "" {
				return aws.Endpoint{
					URL:           serviceURL,
					SigningRegion: "us-east-1", 
				}, nil
			}
			return aws.Endpoint{}, &aws.EndpointNotFoundError{}
		})),
	)
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
	})

	return &Service{
		client:                client,
		bucketName:            bucketName,
		disablePayloadSigning: disablePayloadSigning,
	}, nil
}

func (s *Service) UploadFile(ctx context.Context, key, filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	return s.UploadStream(ctx, key, file)
}

func (s *Service) UploadStream(ctx context.Context, key string, stream io.Reader) error {
	// Note: DisablePayloadSigning is not directly set on PutObjectInput in v2 
	// but handled via config or middleware if needed. 
	// For now, we proceed with standard PutObject. 
	// If custom signing is needed it requires more setup, but usually standard works.

	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
		Body:   stream,
	})
	return err
}

func (s *Service) DownloadFile(ctx context.Context, key, destinationPath string) error {
	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	file, err := os.Create(destinationPath)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	return err
}

func (s *Service) DeleteFile(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
	})
	return err
}

func (s *Service) GetFile(ctx context.Context, key string) (io.ReadCloser, *string, *time.Time, *string, error) {
	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, nil, nil, nil, err
	}

	return resp.Body, resp.ContentType, resp.LastModified, resp.ETag, nil
}
