using System.Text.RegularExpressions;

namespace LingosBotApp;

internal static class ChallengeMatcher
{
    // The dashboard has used small wording and punctuation variations for the
    // same challenge. Match the meaning of the condition, not one serialized
    // description. The normalized input has no diacritics and punctuation is
    // represented by spaces.
    private static readonly Regex MaxErrorLimit = new(
        @"\b(?:maks(?:ymalnie)?|max(?:imum)?|najwyzej|nie wiecej niz|do|at most|no more than)\s+(?<count>\d+|[a-z]+)\s+(?:blad\w*|bled\w*|error\w*)\b",
        RegexOptions.Compiled | RegexOptions.CultureInvariant);

    private static readonly Regex NoErrorLimit = new(
        @"\bbez\s+(?:blad\w*|bled\w*|error\w*)\b",
        RegexOptions.Compiled | RegexOptions.CultureInvariant);

    private static readonly Regex LessonWord = new(
        @"\blekcj\w*\b|\blesson\w*\b",
        RegexOptions.Compiled | RegexOptions.CultureInvariant);

    private static readonly Regex NegatedKeyword = new(
        @"\b(?:nie|bez|brak)\s+perfekcjonizm\w*\b",
        RegexOptions.Compiled | RegexOptions.CultureInvariant);

    private static readonly Regex PerfectionismKeywordPattern = new(
        @"\bperfekcjonizm\w*\b",
        RegexOptions.Compiled | RegexOptions.CultureInvariant);

    public static bool IsPerfectionism(string title, string description)
    {
        var normalizedTitle = TextNormalizer.NormalizeForMatching(title);
        var normalizedDescription = TextNormalizer.NormalizeForMatching(description);
        var allText = $"{normalizedTitle} {normalizedDescription}";
        var keywordInTitle = ContainsKeyword(normalizedTitle);
        var keywordInDescription = ContainsKeyword(normalizedDescription);

        if (!keywordInTitle && !keywordInDescription)
        {
            return false;
        }

        if (NegatedKeyword.IsMatch(allText))
        {
            return false;
        }

        var errorLimit = MaxErrorLimit.Match(allText);
        var hasNoErrorLimit = NoErrorLimit.IsMatch(allText);
        if (!errorLimit.Success && !hasNoErrorLimit)
        {
            // The challenge name in the title is the strongest signal. This is
            // also important when a dashboard variant omits its description.
            return keywordInTitle;
        }

        var errorCount = errorLimit.Groups["count"].Value;
        var isOneOrFewerErrors = hasNoErrorLimit ||
            (int.TryParse(errorCount, out var numericCount)
                ? numericCount <= 1
                : errorCount.StartsWith("jed", StringComparison.Ordinal) ||
                  errorCount.StartsWith("zer", StringComparison.Ordinal) ||
                  errorCount == "one" ||
                  errorCount == "zero");

        // A description-only name is accepted only when it is accompanied by
        // the lesson/max-one-error condition, avoiding generic mentions of the
        // word "perfekcjonizm" in another challenge's description. For a name
        // in the title, this check also prevents a contradictory max-two (etc.)
        // condition from disabling intentional errors.
        return isOneOrFewerErrors && LessonWord.IsMatch(allText);
    }

    private static bool ContainsKeyword(string normalizedText) =>
        PerfectionismKeywordPattern.IsMatch(normalizedText);
}
