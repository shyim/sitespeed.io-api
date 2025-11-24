using Amazon.S3;
using Amazon.S3.Model;
using sitespeed_service.Infrastructure;

namespace sitespeed_service.Services;

public class StorageService
{
    private readonly IAmazonS3 _s3Client;
    private readonly string _bucketName;
    private readonly bool _disablePayloadSigning;

    public StorageService(IConfiguration configuration)
    {
        var serviceUrl = configuration["S3_SERVICE_URL"];
        var accessKey = configuration["S3_ACCESS_KEY"];
        var secretKey = configuration["S3_SECRET_KEY"];
        _bucketName = configuration["S3_BUCKET_NAME"] ?? "sitespeed-results";
        _disablePayloadSigning = configuration["S3_DISABLE_PAYLOAD_SIGNING"] != "false";

        var config = new AmazonS3Config
        {
            ServiceURL = serviceUrl,
            ForcePathStyle = true
        };

        _s3Client = new AmazonS3Client(accessKey, secretKey, config);
    }

    public async Task UploadFileAsync(string key, string filePath)
    {
        var putRequest = new PutObjectRequest
        {
            BucketName = _bucketName,
            Key = key,
            FilePath = filePath,
            DisablePayloadSigning = _disablePayloadSigning
        };

        await _s3Client.PutObjectAsync(putRequest);
    }

    public async Task UploadStreamAsync(string key, Stream stream)
    {
        var putRequest = new PutObjectRequest
        {
            BucketName = _bucketName,
            Key = key,
            InputStream = stream,
            DisablePayloadSigning = _disablePayloadSigning
        };

        await _s3Client.PutObjectAsync(putRequest);
    }

    public async Task DownloadFileAsync(string key, string destinationPath)
    {
        var getRequest = new GetObjectRequest
        {
            BucketName = _bucketName,
            Key = key
        };

        using var response = await _s3Client.GetObjectAsync(getRequest);
        using var fileStream = File.Create(destinationPath);
        await response.ResponseStream.CopyToAsync(fileStream);
    }

    public async Task DeleteFileAsync(string key)
    {
        var deleteRequest = new DeleteObjectRequest
        {
            BucketName = _bucketName,
            Key = key
        };

        await _s3Client.DeleteObjectAsync(deleteRequest);
    }

    public async Task<(Stream Stream, string ContentType, DateTimeOffset? LastModified, string ETag)> GetFileAsync(string key)
    {
        var getRequest = new GetObjectRequest
        {
            BucketName = _bucketName,
            Key = key
        };

        var response = await _s3Client.GetObjectAsync(getRequest);
        DateTimeOffset? lastModified = response.LastModified.HasValue ? new DateTimeOffset(response.LastModified.Value) : null;
        return (new DisposableStream(response.ResponseStream, response), response.Headers.ContentType, lastModified, response.ETag);
    }
}
