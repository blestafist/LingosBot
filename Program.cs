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
                InteractiveConfiguration.Configure(
                    config,
                    Console.In,
                    Console.Out,
                    () => new LingosBot(config, new BrowserFactory()).ScanClasses());
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

}
