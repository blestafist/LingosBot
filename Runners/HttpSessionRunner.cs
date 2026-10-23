using System.Diagnostics;
using System.Text.Json;

namespace LingosBotApp;

// Current-site backend for visible-login runs. Fail closed on changed payloads instead of
// silently skipping classes, challenges or vocabulary, or replaying a lesson.
internal sealed class HttpSessionRunner(LingosHttpSession session, AppConfig config)
{
    public JsonElement ReadDashboard(string? expectedGroup = null)
    {
        var page = session.GetPage(config.StudentDashboardUrl);
        if (page.GetProperty("page").GetString() != "dashboard") throw InvalidData();
        var dashboard = page.GetProperty("dashboard");
        if (expectedGroup is not null && GroupId(dashboard) != expectedGroup) throw InvalidData();
        _ = dashboard.GetProperty("lessonLimitReached").GetBoolean();
        _ = ParseChallenges(dashboard);
        return dashboard;
    }

    public int Run(JsonElement initialDashboard)
    {
        var classes = initialDashboard.GetProperty("groups").EnumerateArray().Select(group => new ClassInfo(
            TextNormalizer.Normalize(group.GetProperty("name").GetString() ?? ""),
            config.StudentDashboardUrl, group.GetProperty("id").GetInt64().ToString(System.Globalization.CultureInfo.InvariantCulture)))
            .ToList();
        if (classes.Count == 0 || classes.Any(c => string.IsNullOrWhiteSpace(c.Title) || !long.TryParse(c.GroupId, out var id) || id <= 0) ||
            classes.Select(c => c.GroupId).Distinct().Count() != classes.Count) throw InvalidData();
        Console.WriteLine($"Found {classes.Count} class(es) to process through HTTP.");
        var completed = 0;
        for (var index = 0; index < classes.Count; index++)
        {
            var @class = classes[index];
            var count = ClassLessonConfiguration.ResolveCount(config, @class, classes);
            Console.WriteLine($"\n=== Class {index + 1}/{classes.Count}: {@class.Title} ({count} lesson(s)) ===");
            if (count == 0) continue;
            var dashboard = ReadDashboard();
            if (GroupId(dashboard) != @class.GroupId)
            {
                var id = long.Parse(@class.GroupId!, System.Globalization.CultureInfo.InvariantCulture);
                if (dashboard.TryGetProperty("contexts", out var contexts) && contexts.ValueKind == JsonValueKind.Object)
                    session.Post("/student/context", new { kind = "group", id });
                else
                    session.Post("/student/group", new { groupId = id });
                dashboard = ReadDashboard(@class.GroupId);
            }
            if (dashboard.GetProperty("lessonLimitReached").GetBoolean())
            {
                Console.WriteLine("Daily lesson limit reached. Skipping this class.");
                continue;
            }
            var answers = new LessonAnswers(config, CollectVocabulary(@class.GroupId!));
            for (var lesson = 1; lesson <= count; lesson++)
            {
                try
                {
                    // Fresh per lesson, including after completion/join; never
                    // reuse initial DOM props or stale challenge status.
                    dashboard = ReadDashboard(@class.GroupId);
                    if (dashboard.GetProperty("lessonLimitReached").GetBoolean()) throw new LessonLimitReachedException("Daily lesson limit reached.");
                    var perfectionism = EnsureChallenge(dashboard, @class.GroupId!);
                    var watch = Stopwatch.StartNew();
                    Console.WriteLine($"Preparing lesson {lesson} through HTTP...");
                    session.Navigate($"/learning/start/0?groupId={@class.GroupId}");
                    var initial = session.TryRead() ?? throw InvalidData();
                    new ApiLessonRunner(session, config, answers.Resolve, () => answers.MakeError(perfectionism), answers.WrongAnswer,
                        refreshDashboardOnCompletion: false)
                        .Run(lesson, initial);
                    completed++;
                    Console.WriteLine($"Lesson {lesson} elapsed time: {watch.Elapsed:mm\\:ss}.");
                }
                catch (LessonLimitReachedException ex)
                {
                    Console.WriteLine($"{ex.Message} Skipping the remaining lessons for '{@class.Title}'.");
                    break;
                }
            }
        }
        Console.WriteLine($"Completed {completed} lesson(s) across {classes.Count} class(es).");
        return completed;
    }

    internal static ChallengeSnapshot ParseChallenges(JsonElement dashboard)
    {
        var challenges = new List<ChallengeInfo>();
        foreach (var item in dashboard.GetProperty("challenges").EnumerateArray())
        {
            var id = item.GetProperty("id").GetInt64();
            var title = TextNormalizer.Normalize(item.GetProperty("title").GetString() ?? "");
            var status = item.GetProperty("status").GetString();
            if (id <= 0 || title.Length == 0 || status is not ("available" or "in_progress" or "completed")) throw InvalidData();
            challenges.Add(new ChallengeInfo(title, item.GetProperty("prize").GetInt32(),
                status == "available" ? $"/student/challenges/{id}" : "", status == "completed",
                TextNormalizer.Normalize(item.GetProperty("description").GetString() ?? "")));
        }
        return new ChallengeSnapshot(challenges);
    }

    private bool EnsureChallenge(JsonElement dashboard, string group)
    {
        var snapshot = ParseChallenges(dashboard);
        if (snapshot.Active is null && snapshot.BestAvailable is { } best)
        {
            Console.WriteLine($"Picking challenge '{best.Title}' (worth {best.Points} pkt)...");
            session.Post(best.JoinUrl);
            var fresh = ReadDashboard(group);
            snapshot = ParseChallenges(fresh);
            if (!fresh.GetProperty("challenges").EnumerateArray().Any(c =>
                $"/student/challenges/{c.GetProperty("id").GetInt64()}" == best.JoinUrl &&
                c.GetProperty("status").GetString() == "in_progress"))
                throw new InvalidOperationException("HTTP challenge join was not confirmed. Stopped before starting the lesson.");
        }
        if (snapshot.ActivePerfectionism is { } perfectionism)
        {
            Console.WriteLine($"Challenge in progress: '{perfectionism.Title}' - intentional errors are disabled for this lesson.");
            return true;
        }
        if (snapshot.Active is { } active) Console.WriteLine($"Challenge in progress: '{active.Title}'.");
        else Console.WriteLine("No challenge available to pick right now - running a normal lesson.");
        return false;
    }

    private IReadOnlyDictionary<string, IReadOnlyList<string>> CollectVocabulary(string group)
    {
        var page = session.GetPage("/student/wordsets");
        if (page.GetProperty("page").GetString() != "wordsets") throw InvalidData();
        var list = page.GetProperty("wordsets");
        if (GroupId(list) != group) throw InvalidData();
        var vocabulary = new Dictionary<string, List<string>>(StringComparer.OrdinalIgnoreCase);
        var sets = list.GetProperty("wordsets").EnumerateArray().ToList();
        // Sequential small GETs keep session cookie updates ordered. No assets,
        // React execution, browser navigation or persistent vocabulary cache.
        foreach (var set in sets)
        {
            var id = set.GetProperty("id").GetInt64();
            if (id <= 0) throw InvalidData();
            var detail = session.GetPage($"/student/wordsets/{id}");
            if (detail.GetProperty("page").GetString() != "wordset") throw InvalidData();
            var data = detail.GetProperty("wordset");
            if (data.GetProperty("group").GetProperty("id").GetInt64().ToString(System.Globalization.CultureInfo.InvariantCulture) != group ||
                data.GetProperty("wordset").GetProperty("id").GetInt64() != id) throw InvalidData();
            var cards = data.GetProperty("flashcards");
            if (set.TryGetProperty("counts", out var counts) && counts.TryGetProperty("total", out var total) && total.GetInt32() != cards.GetArrayLength())
                throw new InvalidOperationException("Incomplete HTTP vocabulary response. Stopped without guessing.");
            foreach (var card in cards.EnumerateArray())
            {
                var word = TextNormalizer.Normalize(card.GetProperty("word").GetString() ?? "");
                var translation = TextNormalizer.Normalize(card.GetProperty("translation").GetString() ?? "");
                if (word.Length == 0 || translation.Length == 0) throw InvalidData();
                if (!vocabulary.TryGetValue(translation, out var candidates)) vocabulary[translation] = candidates = [];
                if (!candidates.Contains(word, StringComparer.OrdinalIgnoreCase)) candidates.Add(word);
            }
            Console.WriteLine($"Collected {cards.GetArrayLength()} entries from {set.GetProperty("name").GetString()} through HTTP.");
        }
        if (vocabulary.Count == 0) throw new InvalidOperationException("No HTTP vocabulary collected. Stopped without guessing.");
        Console.WriteLine($"Collected {vocabulary.Count} unique Polish prompts with {vocabulary.Sum(p => p.Value.Count)} stored answer candidates.");
        return vocabulary.ToDictionary(p => p.Key, p => (IReadOnlyList<string>)p.Value.AsReadOnly(), StringComparer.OrdinalIgnoreCase);
    }

    private static string GroupId(JsonElement data) => data.GetProperty("currentGroup").GetProperty("id").GetInt64().ToString(System.Globalization.CultureInfo.InvariantCulture);
    private static InvalidOperationException InvalidData() => new("Unrecognized or mismatched Lingos HTTP data. Stopped safely; headless=true restores browser mode.");
}
