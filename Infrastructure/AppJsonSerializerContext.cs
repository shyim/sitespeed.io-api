using System.Text.Json.Serialization;
using sitespeed_service.Models;

namespace sitespeed_service.Infrastructure;

[JsonSerializable(typeof(ErrorResponse))]
[JsonSerializable(typeof(AnalyzeRequest))]
[JsonSerializable(typeof(ApiAnalyzeRequest))]
[JsonSerializable(typeof(AnalyzeResponse))]
[JsonSerializable(typeof(BrowserTime))]
[JsonSerializable(typeof(PageXray))]
internal partial class AppJsonSerializerContext : JsonSerializerContext
{
}
