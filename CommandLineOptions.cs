namespace LingosBotApp;

internal sealed record CommandLineOptions
{
    public string? ConfigFilePath { get; private init; }
    public bool ScanClasses { get; private init; }
    public bool RunConfiguration { get; private init; }
    public string? Email { get; private init; }
    public string? Password { get; private init; }
    public bool? SetHeadless { get; private init; }
    public bool? RunHeadless { get; private init; }
    public string? Browser { get; private init; }
    public string? SetBrowser { get; private init; }
    public int? ErrorsPer100Words { get; private init; }
    public string? BrowserBinaryPath { get; private init; }

    public bool HasConfigurationChanges => Email is not null || Password is not null || SetHeadless.HasValue ||
        SetBrowser is not null || ErrorsPer100Words.HasValue || BrowserBinaryPath is not null;

    public static CommandLineOptions Parse(string[] args)
    {
        var options = new CommandLineOptions();

        for (var index = 0; index < args.Length; index++)
        {
            var argument = args[index];
            string ReadValue()
            {
                if (++index >= args.Length || args[index].StartsWith('-'))
                {
                    throw new ArgumentException($"{argument} requires a value.");
                }

                return args[index];
            }

            options = argument switch
            {
                "--scan_classes" or "-s" => options with { ScanClasses = true },
                "--config" or "-c" => options with { ConfigFilePath = ReadValue() },
                "--set_email" => options with { Email = ReadValue() },
                "--set_passwd" => options with { Password = ReadValue() },
                "--set_headless" => options with { SetHeadless = ParseBoolean(argument, ReadValue()) },
                "--headless" or "-h" => options with { RunHeadless = true },
                "--visible" or "-v" => options with { RunHeadless = false },
                "--browser" => options with { Browser = ReadValue() },
                "--set_browser" => options with { SetBrowser = ReadValue() },
                "--set_errors" => options with { ErrorsPer100Words = ParseNonNegativeInteger(argument, ReadValue()) },
                "--set_browser_path" => options with { BrowserBinaryPath = ReadValue() },
                "--run_config" => options with { RunConfiguration = true },
                _ => throw new ArgumentException($"Unknown argument: {argument}")
            };
        }

        if (options.RunHeadless.HasValue && args.Any(argument => argument is "--headless" or "-h") &&
            args.Any(argument => argument is "--visible" or "-v"))
        {
            throw new ArgumentException("Use either --headless or --visible, not both.");
        }

        return options;
    }

    private static bool ParseBoolean(string argument, string value) => value.ToLowerInvariant() switch
    {
        "true" => true,
        "false" => false,
        _ => throw new ArgumentException($"{argument} must be true or false.")
    };

    private static int ParseNonNegativeInteger(string argument, string value)
    {
        if (!int.TryParse(value, out var result) || result is < 0 or > 100)
        {
            throw new ArgumentException($"{argument} must be an integer between 0 and 100.");
        }

        return result;
    }
}
