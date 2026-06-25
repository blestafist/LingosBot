namespace LingosBot;

internal sealed class AppConfig
{
    private readonly Config _config;

    public AppConfig(Config config)
    {
        _config = config;
    }

    // URLs
    public string BaseUrl { get; } = "https://lingos.pl";
    public string LoginUrl { get; } = "https://lingos.pl/h/login";
    public string StudentDashboardUrl { get; } = "https://lingos.pl/student-confirmed/group";

    // User configuration from config.json
    public bool AutomaticLogin => _config.automaticLogin;
    public string Email => _config.email;
    public string Password => _config.password;
    public int NumberOfLessons => _config.numberOfLessons;
    public string Browser => _config.browser;
    public bool Headless => _config.headless;
    public string WordsDataBasePath => _config.wordsDataBasePath;
    public int ErrorsPer100Words => _config.errorsPer100Words;

    // Timeouts
    public TimeSpan DefaultWaitTimeout { get; } = TimeSpan.FromSeconds(15);
    public TimeSpan ShortWaitTimeout { get; } = TimeSpan.FromSeconds(4);
    public TimeSpan PageLoadTimeout { get; } = TimeSpan.FromSeconds(60);
    public TimeSpan PollingInterval { get; } = TimeSpan.FromMilliseconds(25);

    // Limits
    public int MinLessonCount { get; } = 1;
    public int LessonPromptSafetyCap { get; } = 30;
}
