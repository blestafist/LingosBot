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
    public string StudentDashboardUrl { get; set; } = "https://lingos.pl/student/dashboard";
    public AppCredentials? Credentials { get; set; }
    public string? BrowserBinaryPath { get; set; }
    public string Browser { get; set; } = "Chrome";
    public bool Headless { get; set; }
    public int ErrorsPer100Words { get; set; } = 10;
    [System.Text.Json.Serialization.JsonIgnore(Condition = System.Text.Json.Serialization.JsonIgnoreCondition.WhenWritingNull)]
    public int? DefaultWaitTimeoutSeconds { get; set; }
    [System.Text.Json.Serialization.JsonIgnore(Condition = System.Text.Json.Serialization.JsonIgnoreCondition.WhenWritingNull)]
    public int? ShortWaitTimeoutSeconds { get; set; }
    [System.Text.Json.Serialization.JsonIgnore(Condition = System.Text.Json.Serialization.JsonIgnoreCondition.WhenWritingNull)]
    public int? LessonRestartReuseTimeoutMilliseconds { get; set; }
    [System.Text.Json.Serialization.JsonIgnore(Condition = System.Text.Json.Serialization.JsonIgnoreCondition.WhenWritingNull)]
    public int? PageLoadTimeoutSeconds { get; set; }
    [System.Text.Json.Serialization.JsonIgnore(Condition = System.Text.Json.Serialization.JsonIgnoreCondition.WhenWritingNull)]
    public int? PollingIntervalMilliseconds { get; set; }
    public int LessonCount { get; set; } = 1;
    public Dictionary<string, int> ClassLessonCounts { get; set; } = new(StringComparer.Ordinal);
    [System.Text.Json.Serialization.JsonIgnore(Condition = System.Text.Json.Serialization.JsonIgnoreCondition.WhenWritingNull)]
    public int? LessonPromptSafetyCap { get; set; }
    [System.Text.Json.Serialization.JsonIgnore(Condition = System.Text.Json.Serialization.JsonIgnoreCondition.WhenWritingNull)]
    public int? ChallengeLessonSafetyCap { get; set; }

    [System.Text.Json.Serialization.JsonIgnore]
    public TimeSpan DefaultWaitTimeout => TimeSpan.FromSeconds(DefaultWaitTimeoutSeconds ?? 15);
    [System.Text.Json.Serialization.JsonIgnore]
    public TimeSpan ShortWaitTimeout => TimeSpan.FromSeconds(ShortWaitTimeoutSeconds ?? 4);
    [System.Text.Json.Serialization.JsonIgnore]
    public TimeSpan LessonRestartReuseTimeout => TimeSpan.FromMilliseconds(LessonRestartReuseTimeoutMilliseconds ?? 1500);
    [System.Text.Json.Serialization.JsonIgnore]
    public TimeSpan PageLoadTimeout => TimeSpan.FromSeconds(PageLoadTimeoutSeconds ?? 60);
    [System.Text.Json.Serialization.JsonIgnore]
    public TimeSpan PollingInterval => TimeSpan.FromMilliseconds(PollingIntervalMilliseconds ?? 25);
    [System.Text.Json.Serialization.JsonIgnore]
    public int EffectiveLessonPromptSafetyCap => LessonPromptSafetyCap ?? 30;
    [System.Text.Json.Serialization.JsonIgnore]
    public int EffectiveChallengeLessonSafetyCap => ChallengeLessonSafetyCap ?? 40;

    public static string DefaultConfigFilePath => Path.Combine(Environment.CurrentDirectory, "config.json");

    public static AppConfig Load(string? configFilePath = null, bool validate = true)
    {
        var path = ResolveConfigFilePath(configFilePath);

        if (!File.Exists(path))
        {
            var config = new AppConfig();
            Save(config, path);

            if (validate)
            {
                throw new InvalidOperationException(
                    $"Created configuration file: {path}. Set credentials and lessonCount before starting the bot.");
            }

            return config;
        }

        try
        {
            var json = File.ReadAllText(path);
            var config = JsonSerializer.Deserialize<AppConfig>(json, SerializerOptions)
                ?? throw new JsonException("The configuration is empty.");

            if (validate)
            {
                config.Validate();
            }

            return config;
        }
        catch (Exception ex) when (ex is IOException or JsonException)
        {
            throw new InvalidOperationException($"Could not load configuration from '{path}': {ex.Message}", ex);
        }
    }

    public static void Save(AppConfig config, string? configFilePath = null)
    {
        var path = ResolveConfigFilePath(configFilePath);
        var directory = Path.GetDirectoryName(path);

        if (!string.IsNullOrEmpty(directory))
        {
            Directory.CreateDirectory(directory);
        }

        File.WriteAllText(path, JsonSerializer.Serialize(config, SerializerOptions));
    }

    public void Validate()
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

        if (ErrorsPer100Words is < 0 or > 100 || DefaultWaitTimeoutSeconds is <= 0 || ShortWaitTimeoutSeconds is <= 0 ||
            LessonRestartReuseTimeoutMilliseconds is <= 0 || PageLoadTimeoutSeconds is <= 0 ||
            PollingIntervalMilliseconds is <= 0 || LessonCount < 1 || LessonPromptSafetyCap is <= 0 ||
            ChallengeLessonSafetyCap is <= 0)
        {
            throw new InvalidOperationException("Numeric configuration values must be positive; errorsPer100Words must be between 0 and 100.");
        }
    }

    private static string ResolveConfigFilePath(string? configFilePath) => Path.GetFullPath(
        string.IsNullOrWhiteSpace(configFilePath) ? DefaultConfigFilePath : configFilePath);
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
