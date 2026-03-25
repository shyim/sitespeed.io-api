package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/shyim/sitespeed-api/internal/observability"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
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
	)
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = true
		if cfg.ServiceURL != "" {
			o.BaseEndpoint = aws.String(cfg.ServiceURL)
		}
	})

	return &Service{
		client:                client,
		bucketName:            cfg.BucketName,
		disablePayloadSigning: cfg.DisablePayloadSigning,
	}, nil
}

func (s *Service) UploadFile(ctx context.Context, key, filePath string) error {
	ctx, span := observability.Tracer("storage").Start(ctx, "storage.UploadFile")
	defer span.End()
	span.SetAttributes(
		attribute.String("storage.bucket", s.bucketName),
		attribute.String("storage.key", key),
		attribute.String("file.path", filePath),
	)

	file, err := os.Open(filePath)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to open upload file")
		return err
	}
	defer func() { _ = file.Close() }()

	return s.UploadStream(ctx, key, file)
}

func (s *Service) UploadStream(ctx context.Context, key string, stream io.Reader) error {
	ctx, span := observability.Tracer("storage").Start(ctx, "storage.UploadStream")
	defer span.End()
	span.SetAttributes(
		attribute.String("storage.bucket", s.bucketName),
		attribute.String("storage.key", key),
	)

	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
		Body:   stream,
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to upload object")
		return fmt.Errorf("upload %s: %w", key, err)
	}

	return err
}

func (s *Service) DownloadFile(ctx context.Context, key, destinationPath string) error {
	ctx, span := observability.Tracer("storage").Start(ctx, "storage.DownloadFile")
	defer span.End()
	span.SetAttributes(
		attribute.String("storage.bucket", s.bucketName),
		attribute.String("storage.key", key),
		attribute.String("file.path", destinationPath),
	)

	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to download object")
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	file, err := os.Create(destinationPath)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create destination file")
		return err
	}
	defer func() { _ = file.Close() }()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to write downloaded object")
	}

	return err
}

func (s *Service) DeleteFile(ctx context.Context, key string) error {
	ctx, span := observability.Tracer("storage").Start(ctx, "storage.DeleteFile")
	defer span.End()
	span.SetAttributes(
		attribute.String("storage.bucket", s.bucketName),
		attribute.String("storage.key", key),
	)

	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to delete object")
	}

	return err
}

func (s *Service) GetFile(ctx context.Context, key string) (io.ReadCloser, *string, *time.Time, *string, error) {
	ctx, span := observability.Tracer("storage").Start(ctx, "storage.GetFile")
	defer span.End()
	span.SetAttributes(
		attribute.String("storage.bucket", s.bucketName),
		attribute.String("storage.key", key),
	)

	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get object")
		return nil, nil, nil, nil, err
	}

	return resp.Body, resp.ContentType, resp.LastModified, resp.ETag, nil
}
