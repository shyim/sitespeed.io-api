package testhelper

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/grafana/s3-mock"
	"github.com/shyim/sitespeed-api/internal/storage"
	"github.com/stretchr/testify/require"
)

const BucketName = "test-bucket"

// StartS3Mock starts an in-memory S3 server and returns a storage config
// pointed at it.
//
// The server runs inside the test process, so unlike the MinIO testcontainer it
// needs no Docker, no image pull and no network. Each call gets its own isolated
// server, so tests cannot leak objects into one another.
func StartS3Mock(t *testing.T, ctx context.Context) storage.Config {
	t.Helper()

	client, closeFn, err := s3mock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = closeFn(ctx) })

	// s3mock does not export the address it listens on, but the client it hands
	// back carries it, which is enough to point a second client at the same
	// server.
	endpoint := client.Options().BaseEndpoint
	require.NotNil(t, endpoint, "s3-mock client has no base endpoint")

	// The mock does not auto-create buckets, and it answers requests for a
	// missing bucket with NoSuchBucket rather than NoSuchKey, so the bucket has
	// to exist for the not-found cases to exercise the right path.
	_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(BucketName),
	})
	require.NoError(t, err)

	// s3mock does not check credentials; these only have to be non-empty for
	// the AWS SDK to sign requests.
	return storage.Config{
		ServiceURL:            *endpoint,
		AccessKey:             "test-access-key",
		SecretKey:             "test-secret-key",
		BucketName:            BucketName,
		DisablePayloadSigning: true,
	}
}
