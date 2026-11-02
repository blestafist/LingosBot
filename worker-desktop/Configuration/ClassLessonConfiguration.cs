namespace LingosBotApp;

internal static class ClassLessonConfiguration
{
    public static string GetKey(ClassInfo @class)
    {
        if (!string.IsNullOrWhiteSpace(@class.GroupId))
        {
            return $"group:{@class.GroupId.Trim()}";
        }

        return $"url:{@class.ChangeUrl.Trim()}";
    }

    public static int ResolveCount(
        AppConfig config,
        ClassInfo @class,
        IReadOnlyList<ClassInfo> discoveredClasses)
    {
        var counts = config.ClassLessonCounts ?? new Dictionary<string, int>(StringComparer.Ordinal);
        var key = GetKey(@class);

        if (counts.TryGetValue(key, out var configuredCount))
        {
            return configuredCount;
        }

        // Configurations created before stable class keys were introduced used
        // the title. Only use such an entry when the current class list makes
        // that title unambiguous; otherwise a duplicate title must not inherit
        // another class's setting.
        var titleMatches = discoveredClasses.Count(candidate =>
            string.Equals(candidate.Title, @class.Title, StringComparison.OrdinalIgnoreCase));

        if (titleMatches == 1 && TryGetCaseInsensitive(counts, @class.Title, out var legacyCount))
        {
            return legacyCount;
        }

        return config.LessonCount;
    }

    public static int? FindPreviousCount(
        IReadOnlyDictionary<string, int> counts,
        ClassInfo @class,
        IReadOnlyList<ClassInfo> discoveredClasses)
    {
        if (counts.TryGetValue(GetKey(@class), out var configuredCount))
        {
            return configuredCount;
        }

        var titleMatches = discoveredClasses.Count(candidate =>
            string.Equals(candidate.Title, @class.Title, StringComparison.OrdinalIgnoreCase));

        return titleMatches == 1 && TryGetCaseInsensitive(counts, @class.Title, out var legacyCount)
            ? legacyCount
            : null;
    }

    public static string GetPromptLabel(ClassInfo @class, IReadOnlyList<ClassInfo> discoveredClasses)
    {
        var duplicateTitle = discoveredClasses.Count(candidate =>
            string.Equals(candidate.Title, @class.Title, StringComparison.OrdinalIgnoreCase)) > 1;

        if (!duplicateTitle)
        {
            return @class.Title;
        }

        var identifier = !string.IsNullOrWhiteSpace(@class.GroupId)
            ? $"ID {@class.GroupId.Trim()}"
            : @class.ChangeUrl.Trim();
        return $"{@class.Title} ({identifier})";
    }

    private static bool TryGetCaseInsensitive(
        IReadOnlyDictionary<string, int> counts,
        string key,
        out int value)
    {
        foreach (var pair in counts)
        {
            if (string.Equals(pair.Key, key, StringComparison.OrdinalIgnoreCase))
            {
                value = pair.Value;
                return true;
            }
        }

        value = default;
        return false;
    }
}
