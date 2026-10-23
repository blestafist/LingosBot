namespace LingosBotApp;

// Shared by UI, browser API and HTTP-only lessons.
internal sealed class LessonAnswers(AppConfig config, IReadOnlyDictionary<string, IReadOnlyList<string>> vocabulary)
{
    private readonly Random _random = new();
    private readonly IReadOnlyDictionary<string, IReadOnlyList<string>> _reverse = BuildReverseVocabulary(vocabulary);

    public IReadOnlyList<string> Resolve(string prompt)
    {
        if (vocabulary.TryGetValue(prompt, out var direct)) return direct;
        if (_reverse.TryGetValue(prompt, out var reverse))
        {
            Console.WriteLine($"Prompt '{prompt}' matched the reverse vocabulary lookup.");
            return reverse;
        }
        return [];
    }

    public bool MakeError(bool perfectionismActive) => _random.NextDouble() <
        ErrorRateResolver.GetEffectiveRate(config.ErrorsPer100Words, perfectionismActive) / 100.0;

    public string WrongAnswer(string answer)
    {
        if (string.IsNullOrEmpty(answer)) return "wrong";
        var chars = answer.ToCharArray();
        var index = _random.Next(chars.Length);
        char replacement;
        do { replacement = (char)('a' + _random.Next(26)); }
        while (char.ToUpperInvariant(replacement) == char.ToUpperInvariant(chars[index]));
        chars[index] = replacement;
        return new string(chars);
    }

    private static IReadOnlyDictionary<string, IReadOnlyList<string>> BuildReverseVocabulary(
        IReadOnlyDictionary<string, IReadOnlyList<string>> vocabulary)
    {
        var reverse = new Dictionary<string, List<string>>(StringComparer.OrdinalIgnoreCase);
        foreach (var pair in vocabulary)
        foreach (var foreignWord in pair.Value)
        {
            var word = TextNormalizer.Normalize(foreignWord);
            if (string.IsNullOrWhiteSpace(word)) continue;
            if (!reverse.TryGetValue(word, out var candidates)) reverse[word] = candidates = [];
            if (!candidates.Contains(pair.Key, StringComparer.OrdinalIgnoreCase)) candidates.Add(pair.Key);
        }
        return reverse.ToDictionary(p => p.Key, p => (IReadOnlyList<string>)p.Value.AsReadOnly(), StringComparer.OrdinalIgnoreCase);
    }
}
