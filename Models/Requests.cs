namespace sitespeed_service.Models;

public record AnalyzeRequest(string Id, string[] Urls);
public record ApiAnalyzeRequest(string[] Urls);
