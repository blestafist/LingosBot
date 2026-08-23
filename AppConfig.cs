using System.Text.Json;
using System.Text.RegularExpressions;

namespace LingosBotApp;

internal sealed class AppConfig
{
    private static readonly JsonSerializerOptions SerializerOptions = new()
    {
        PropertyNamingPolicy = JsonNamingPolicy.CamelCase,
        PropertyNameCaseInsensitive = true,
        WriteIndented = true
    };

    public string BaseUrl { get; set; } = "https://lingos.pl";
    public string StudentDashboardUrl { get; set; } = "https://lingos.pl/student-confirmed/group";
    public AppCredentials? Credentials { get; set; }
    public string? BrowserBinaryPath { get; set; }
    public string Browser { get; set; } = "Chrome";
    public bool Headless { get; set; }
    public int ErrorsPer100Words { get; set; } = 10;
    public int DefaultWaitTimeoutSeconds { get; set; } = 15;
    public int ShortWaitTimeoutSeconds { get; set; } = 4;
    public int LessonRestartReuseTimeoutMilliseconds { get; set; } = 1500;
    public int PageLoadTimeoutSeconds { get; set; } = 60;
    public int PollingIntervalMilliseconds { get; set; } = 25;
    public int LessonCount { get; set; } = 1;
    public Dictionary<string, int> ClassLessonCounts { get; set; } = new(StringComparer.OrdinalIgnoreCase);
    public int LessonPromptSafetyCap { get; set; } = 30;
    public int ChallengeLessonSafetyCap { get; set; } = 40;

    [System.Text.Json.Serialization.JsonIgnore]
    public TimeSpan DefaultWaitTimeout => TimeSpan.FromSeconds(DefaultWaitTimeoutSeconds);
    [System.Text.Json.Serialization.JsonIgnore]
    public TimeSpan ShortWaitTimeout => TimeSpan.FromSeconds(ShortWaitTimeoutSeconds);
    [System.Text.Json.Serialization.JsonIgnore]
    public TimeSpan LessonRestartReuseTimeout => TimeSpan.FromMilliseconds(LessonRestartReuseTimeoutMilliseconds);
    [System.Text.Json.Serialization.JsonIgnore]
    public TimeSpan PageLoadTimeout => TimeSpan.FromSeconds(PageLoadTimeoutSeconds);
    [System.Text.Json.Serialization.JsonIgnore]
    public TimeSpan PollingInterval => TimeSpan.FromMilliseconds(PollingIntervalMilliseconds);

    public static string ConfigFilePath => Path.Combine(Environment.CurrentDirectory, "config.json");

    public static AppConfig Load()
    {
        if (!File.Exists(ConfigFilePath))
        {
            var config = new AppConfig();
            File.WriteAllText(ConfigFilePath, JsonSerializer.Serialize(config, SerializerOptions));
            throw new InvalidOperationException(
                $"Created configuration file: {ConfigFilePath}. Set credentials and lessonCount before starting the bot.");
        }

        try
        {
            var json = File.ReadAllText(ConfigFilePath);
            var config = JsonSerializer.Deserialize<AppConfig>(json, SerializerOptions)
                ?? throw new JsonException("The configuration is empty.");
            config.Validate();
            return config;
        }
        catch (Exception ex) when (ex is IOException or JsonException)
        {
            throw new InvalidOperationException($"Could not load configuration from '{ConfigFilePath}': {ex.Message}", ex);
        }
    }

    private void Validate()
    {
        if (string.IsNullOrWhiteSpace(BaseUrl) || string.IsNullOrWhiteSpace(StudentDashboardUrl))
        {
            throw new InvalidOperationException("baseUrl and studentDashboardUrl must not be empty.");
        }

        if (string.IsNullOrWhiteSpace(Browser))
        {
            throw new InvalidOperationException("browser must not be empty.");
        }

        if (Credentials is null || string.IsNullOrWhiteSpace(Credentials.Email) || string.IsNullOrWhiteSpace(Credentials.Password))
        {
            throw new InvalidOperationException("credentials.email and credentials.password must be set in config.json.");
        }

        if (Browser.Trim().ToLowerInvariant() is not ("chrome" or "firefox" or "edge" or "safari"))
        {
            throw new InvalidOperationException("browser must be one of: Chrome, Firefox, Edge, Safari.");
        }

        if (ClassLessonCounts is null || ClassLessonCounts.Any(pair => string.IsNullOrWhiteSpace(pair.Key) || pair.Value < 0))
        {
            throw new InvalidOperationException("classLessonCounts keys must not be empty and values must be zero or positive.");
        }

        if (ErrorsPer100Words < 0 || DefaultWaitTimeoutSeconds <= 0 || ShortWaitTimeoutSeconds <= 0 ||
            LessonRestartReuseTimeoutMilliseconds <= 0 || PageLoadTimeoutSeconds <= 0 ||
            PollingIntervalMilliseconds <= 0 || LessonCount < 1 || LessonPromptSafetyCap < 1 ||
            ChallengeLessonSafetyCap < 1)
        {
            throw new InvalidOperationException("Numeric configuration values must be positive; errorsPer100Words may be zero.");
        }
    }
}

internal sealed record AppCredentials(string Email, string Password);

internal static class TextNormalizer
{
    private static readonly Regex MultipleWhitespace = new(@"\s+", RegexOptions.Compiled);

    public static string Normalize(string value)
    {
        if (string.IsNullOrWhiteSpace(value))
        {
            return string.Empty;
        }

        var sanitized = value.Replace('\u00A0', ' ').Trim();
        return MultipleWhitespace.Replace(sanitized, " ");
    }
}
