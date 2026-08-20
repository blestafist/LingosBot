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

    public string? ChromeBinaryPath { get; set; }

    public string Browser { get; set; } = "Chrome";

    public bool Headless { get; set; }

    public int ErrorsPer100Words { get; set; } = 10;

    public int DefaultWaitTimeoutSeconds { get; set; } = 15;

    public int ShortWaitTimeoutSeconds { get; set; } = 4;

    public int LessonRestartReuseTimeoutMilliseconds { get; set; } = 1500;

    public int PageLoadTimeoutSeconds { get; set; } = 60;

    public int PollingIntervalMilliseconds { get; set; } = 25;

    public int MinLessonCount { get; set; } = 1;

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
            config.Save();
            Console.WriteLine($"Created configuration file: {ConfigFilePath}");
            return config;
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

    public void Save()
    {
        Validate();
        File.WriteAllText(ConfigFilePath, JsonSerializer.Serialize(this, SerializerOptions));
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

        if (ErrorsPer100Words < 0 || DefaultWaitTimeoutSeconds <= 0 || ShortWaitTimeoutSeconds <= 0 ||
            LessonRestartReuseTimeoutMilliseconds <= 0 || PageLoadTimeoutSeconds <= 0 ||
            PollingIntervalMilliseconds <= 0 || MinLessonCount < 1 || LessonPromptSafetyCap < 1 ||
            ChallengeLessonSafetyCap < 1)
        {
            throw new InvalidOperationException("Numeric configuration values must be positive; errorsPer100Words may be zero.");
        }
    }
}

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
