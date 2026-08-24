namespace LingosBotApp;

internal static class Program
{
    private static int Main(string[] args)
    {
        Console.WriteLine("LingosBot");

        try
        {
            var options = CommandLineOptions.Parse(args);
            var config = AppConfig.Load(options.ConfigFilePath, validate: !options.HasConfigurationChanges && !options.RunConfiguration);

            if (options.RunConfiguration)
            {
                ConfigureInteractively(config);
                AppConfig.Save(config, options.ConfigFilePath);
                Console.WriteLine("Configuration saved.");
                return 0;
            }

            if (options.HasConfigurationChanges)
            {
                ApplyConfigurationChanges(config, options);
                AppConfig.Save(config, options.ConfigFilePath);
                Console.WriteLine("Configuration saved.");
                return 0;
            }

            if (options.RunHeadless.HasValue)
            {
                config.Headless = options.RunHeadless.Value;
            }

            if (options.Browser is not null)
            {
                config.Browser = options.Browser;
            }

            config.Validate();
            var bot = new LingosBot(config, new BrowserFactory());

            if (options.ScanClasses)
            {
                var classes = bot.ScanClasses();
                config.ClassLessonCounts = classes.ToDictionary(@class => @class.Title, _ => config.LessonCount, StringComparer.OrdinalIgnoreCase);
                AppConfig.Save(config, options.ConfigFilePath);
                Console.WriteLine("classLessonCounts saved to configuration.");
                return 0;
            }

            bot.Run(config.LessonCount);
            return 0;
        }
        catch (Exception ex)
        {
            Console.Error.WriteLine($"LingosBot failed: {ex.Message}");
            return 1;
        }
    }

    private static void ApplyConfigurationChanges(AppConfig config, CommandLineOptions options)
    {
        if (options.Email is not null || options.Password is not null)
        {
            var credentials = config.Credentials ?? new AppCredentials(string.Empty, string.Empty);
            config.Credentials = credentials with
            {
                Email = options.Email ?? credentials.Email,
                Password = options.Password ?? credentials.Password
            };
        }

        if (options.SetHeadless.HasValue)
        {
            config.Headless = options.SetHeadless.Value;
        }

        if (options.SetBrowser is not null)
        {
            config.Browser = options.SetBrowser;
        }

        if (options.ErrorsPer100Words.HasValue)
        {
            config.ErrorsPer100Words = options.ErrorsPer100Words.Value;
        }

        if (options.BrowserBinaryPath is not null)
        {
            config.BrowserBinaryPath = options.BrowserBinaryPath;
        }
    }

    private static void ConfigureInteractively(AppConfig config)
    {
        Console.WriteLine("Leave a value blank to keep its current setting.");
        var credentials = config.Credentials ?? new AppCredentials(string.Empty, string.Empty);
        var email = ReadSetting("Email", credentials.Email);
        var password = ReadSetting("Password", credentials.Password, secret: true);
        config.Credentials = new AppCredentials(email, password);

        var headless = ReadSetting("Headless (true/false)", config.Headless.ToString().ToLowerInvariant());
        if (!string.Equals(headless, config.Headless.ToString(), StringComparison.OrdinalIgnoreCase))
        {
            config.Headless = bool.TryParse(headless, out var value)
                ? value
                : throw new ArgumentException("Headless must be true or false.");
        }

        config.Browser = ReadSetting("Browser", config.Browser);
        var errors = ReadSetting("Errors per 100 words", config.ErrorsPer100Words.ToString());
        config.ErrorsPer100Words = int.TryParse(errors, out var errorCount) && errorCount >= 0
            ? errorCount
            : throw new ArgumentException("Errors per 100 words must be a non-negative integer.");
        config.BrowserBinaryPath = ReadSetting("Browser binary path", config.BrowserBinaryPath ?? string.Empty);
    }

    private static string ReadSetting(string label, string currentValue, bool secret = false)
    {
        Console.Write($"{label} [{(secret && !string.IsNullOrEmpty(currentValue) ? "configured" : currentValue)}]: ");
        var value = Console.ReadLine();
        return string.IsNullOrWhiteSpace(value) ? currentValue : value.Trim();
    }
}
