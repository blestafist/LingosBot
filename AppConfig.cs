using System.Text.RegularExpressions;

namespace LingosBotApp;

internal sealed class AppConfig
{
    public string BaseUrl { get; } = "https://lingos.pl";

    public string StudentDashboardUrl { get; } = "https://lingos.pl/student-confirmed/group";

    public string CredentialFilePath { get; } = Path.Combine(AppContext.BaseDirectory, "credentials.json");

    public string? ChromeBinaryPath { get; } = Environment.GetEnvironmentVariable("LINGOS_CHROME_BINARY");

    public string Browser { get; } = Environment.GetEnvironmentVariable("LINGOS_BROWSER") ?? "Chrome";

    public bool Headless { get; } = string.Equals(
        Environment.GetEnvironmentVariable("LINGOS_HEADLESS"),
        "true",
        StringComparison.OrdinalIgnoreCase);

    public int ErrorsPer100Words { get; } = int.TryParse(
        Environment.GetEnvironmentVariable("LINGOS_ERRORS_PER_100"),
        out var errors) ? errors : 10;

    public TimeSpan DefaultWaitTimeout { get; } = TimeSpan.FromSeconds(15);

    public TimeSpan ShortWaitTimeout { get; } = TimeSpan.FromSeconds(4);

    public TimeSpan LessonRestartReuseTimeout { get; } = TimeSpan.FromMilliseconds(1500);

    public TimeSpan PageLoadTimeout { get; } = TimeSpan.FromSeconds(60);

    public TimeSpan PollingInterval { get; } = TimeSpan.FromMilliseconds(25);

    public int MinLessonCount { get; } = 1;

    public int LessonPromptSafetyCap { get; } = 30;

    public int ChallengeLessonSafetyCap { get; } = 40;
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
