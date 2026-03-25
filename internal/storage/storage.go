package storage

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

type Config struct {
	ServiceURL            string
	AccessKey             string
	SecretKey             string
	BucketName            string
	DisablePayloadSigning bool
}

func ConfigFromEnv() Config {
	bucketName := os.Getenv("S3_BUCKET_NAME")
	if bucketName == "" {
		bucketName = "sitespeed-results"
	}
	return Config{
		ServiceURL:            os.Getenv("S3_SERVICE_URL"),
		AccessKey:             os.Getenv("S3_ACCESS_KEY"),
		SecretKey:             os.Getenv("S3_SECRET_KEY"),
		BucketName:            bucketName,
		DisablePayloadSigning: os.Getenv("S3_DISABLE_PAYLOAD_SIGNING") != "false",
	}
}

type Service struct {
	client                *s3.Client
	bucketName            string
	disablePayloadSigning bool
}

func NewService(ctx context.Context) (*Service, error) {
	return NewServiceWithConfig(ctx, ConfigFromEnv())
}

func NewServiceWithConfig(ctx context.Context, cfg Config) (*Service, error) {
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
			return aws.Credentials{
				AccessKeyID:     cfg.AccessKey,
				SecretAccessKey: cfg.SecretKey,
			}, nil
		})),
		config.WithRegion("us-east-1"),
		config.WithEndpointResolverWithOptions(aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			if cfg.ServiceURL != "" {
				return aws.Endpoint{
					URL:           cfg.ServiceURL,
					SigningRegion: "us-east-1",
				}, nil
			}
			return aws.Endpoint{}, &aws.EndpointNotFoundError{}
		})),
	)
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = true
	})

	return &Service{
		client:                client,
		bucketName:            cfg.BucketName,
		disablePayloadSigning: cfg.DisablePayloadSigning,
	}, nil
}

func (s *Service) UploadFile(ctx context.Context, key, filePath string) error {
	ctx, span := otel.Tracer("storage").Start(ctx, "S3.UploadFile")
	span.SetAttributes(attribute.String("s3.key", key))
	defer span.End()

	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	return s.UploadStream(ctx, key, file)
}

func (s *Service) UploadStream(ctx context.Context, key string, stream io.Reader) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
		Body:   stream,
	})
	return err
}

func (s *Service) DownloadFile(ctx context.Context, key, destinationPath string) error {
	ctx, span := otel.Tracer("storage").Start(ctx, "S3.DownloadFile")
	span.SetAttributes(attribute.String("s3.key", key))
	defer span.End()

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
	ctx, span := otel.Tracer("storage").Start(ctx, "S3.DeleteFile")
	span.SetAttributes(attribute.String("s3.key", key))
	defer span.End()

	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
	})
	return err
}

func (s *Service) GetFile(ctx context.Context, key string) (io.ReadCloser, *string, *time.Time, *string, error) {
	ctx, span := otel.Tracer("storage").Start(ctx, "S3.GetFile")
	span.SetAttributes(attribute.String("s3.key", key))
	defer span.End()

	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, nil, nil, nil, err
	}

	return resp.Body, resp.ContentType, resp.LastModified, resp.ETag, nil
}
