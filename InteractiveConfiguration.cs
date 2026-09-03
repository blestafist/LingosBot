namespace LingosBotApp;

internal static class InteractiveConfiguration
{
    public static void Configure(
        AppConfig config,
        TextReader input,
        TextWriter output,
        Func<IReadOnlyList<ClassInfo>> discoverClasses)
    {
        // Ask this before any other setting. Class discovery starts a browser and
        // logs in, so it must happen only after the user explicitly opts in.
        var configureClassesSeparately = ReadBoolean(
            "Configure each class separately (yes/no)",
            defaultValue: false,
            input,
            output);

        output.WriteLine("Leave a value blank to keep its current setting.");
        var credentials = config.Credentials ?? new AppCredentials(string.Empty, string.Empty);
        var email = ReadSetting("Email", credentials.Email, input, output);
        var password = ReadSetting("Password", credentials.Password, input, output, secret: true);
        config.Credentials = new AppCredentials(email, password);

        var headless = ReadSetting("Headless (true/false)", config.Headless.ToString().ToLowerInvariant(), input, output);
        if (!string.Equals(headless, config.Headless.ToString(), StringComparison.OrdinalIgnoreCase))
        {
            config.Headless = bool.TryParse(headless, out var value)
                ? value
                : throw new ArgumentException("Headless must be true or false.");
        }

        config.Browser = ReadSetting("Browser", config.Browser, input, output);
        var errors = ReadSetting("Errors per 100 words", config.ErrorsPer100Words.ToString(), input, output);
        config.ErrorsPer100Words = int.TryParse(errors, out var errorCount) && errorCount is >= 0 and <= 100
            ? errorCount
            : throw new ArgumentException("Errors per 100 words must be an integer between 0 and 100.");
        config.BrowserBinaryPath = ReadSetting("Browser binary path", config.BrowserBinaryPath ?? string.Empty, input, output);

        var lessonCount = ReadSetting("Total lessons per class", config.LessonCount.ToString(), input, output);
        config.LessonCount = int.TryParse(lessonCount, out var totalLessonCount) && totalLessonCount >= 1
            ? totalLessonCount
            : throw new ArgumentException("Total lessons per class must be a positive integer.");

        if (!configureClassesSeparately)
        {
            return;
        }

        // Validate the settings needed by Selenium before opening a browser.
        // Main saves only after this method returns, so a failed discovery does
        // not leave a partially updated configuration on disk.
        config.Validate();
        var classes = discoverClasses();
        var previousCounts = config.ClassLessonCounts ?? new(StringComparer.Ordinal);
        // Keep entries for classes that discovery did not return. They may be
        // temporarily absent from the account and should not lose their saved
        // overrides just because this run could not see them.
        var classLessonCounts = new Dictionary<string, int>(previousCounts, StringComparer.Ordinal);

        foreach (var @class in classes)
        {
            var key = ClassLessonConfiguration.GetKey(@class);
            var previousCount = ClassLessonConfiguration.FindPreviousCount(previousCounts, @class, classes);
            var currentCount = previousCount ?? config.LessonCount;
            var label = ClassLessonConfiguration.GetPromptLabel(@class, classes);
            var count = ReadOptionalSetting(
                $"Lessons for '{label}' (global fallback: {config.LessonCount}; default/fallback/- removes override)",
                currentCount.ToString(),
                input,
                output);

            // A blank means that this class should use the global fallback. Do not
            // materialize that fallback as an override for every discovered class.
            if (count is null)
            {
                // Preserve the original key and value for an existing override;
                // blank input has always meant "keep current setting".
                continue;
            }

            if (IsFallbackRequest(count))
            {
                classLessonCounts.Remove(key);
                RemoveLegacyTitleEntries(classLessonCounts, @class, classes);
                continue;
            }

            RemoveLegacyTitleEntries(classLessonCounts, @class, classes);
            classLessonCounts[key] = int.TryParse(count, out var classLessonCount) && classLessonCount >= 0
                ? classLessonCount
                : throw new ArgumentException($"Lessons for '{label}' must be a non-negative integer.");
        }

        config.ClassLessonCounts = classLessonCounts;
    }

    private static bool ReadBoolean(
        string label,
        bool defaultValue,
        TextReader input,
        TextWriter output)
    {
        var value = ReadSetting(label, defaultValue ? "yes" : "no", input, output);
        return value.ToLowerInvariant() switch
        {
            "yes" or "y" or "true" => true,
            "no" or "n" or "false" => false,
            _ => throw new ArgumentException($"{label} must be answered yes or no.")
        };
    }

    private static string ReadSetting(
        string label,
        string currentValue,
        TextReader input,
        TextWriter output,
        bool secret = false)
    {
        output.Write($"{label} [{(secret && !string.IsNullOrEmpty(currentValue) ? "configured" : currentValue)}]: ");
        var value = input.ReadLine();
        return string.IsNullOrWhiteSpace(value) ? currentValue : value.Trim();
    }

    private static string? ReadOptionalSetting(
        string label,
        string currentValue,
        TextReader input,
        TextWriter output)
    {
        output.Write($"{label} [{currentValue}]: ");
        var value = input.ReadLine();
        return string.IsNullOrWhiteSpace(value) ? null : value.Trim();
    }

    private static bool IsFallbackRequest(string value) =>
        value.Equals("default", StringComparison.OrdinalIgnoreCase) ||
        value.Equals("fallback", StringComparison.OrdinalIgnoreCase) ||
        value == "-";

    private static void RemoveLegacyTitleEntries(
        IDictionary<string, int> counts,
        ClassInfo @class,
        IReadOnlyList<ClassInfo> discoveredClasses)
    {
        // A title key is only associated with a class when that title is unique;
        // retain ambiguous legacy entries rather than deleting an override that
        // cannot safely be attributed to the class being configured.
        var titleMatches = discoveredClasses.Count(candidate =>
            string.Equals(candidate.Title, @class.Title, StringComparison.OrdinalIgnoreCase));

        if (titleMatches != 1)
        {
            return;
        }

        foreach (var key in counts.Keys
                     .Where(key => string.Equals(key, @class.Title, StringComparison.OrdinalIgnoreCase))
                     .ToArray())
        {
            counts.Remove(key);
        }
    }
}
