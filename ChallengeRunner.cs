using System.Text.Json;
using OpenQA.Selenium;
using OpenQA.Selenium.Support.UI;

namespace LingosBotApp;

internal sealed class ChallengeRunner (IWebDriver driver, AppConfig config)
{
    private readonly IWebDriver _driver = driver;
    private readonly AppConfig _config = config;
    private readonly JsonSerializerOptions _jsonOptions = new()
    {
        PropertyNameCaseInsensitive = true
    };


    // Reads challenges from the dashboard. The current UI embeds the challenge
    // model in #app[data-props], while older pages rendered a Bootstrap modal.
    // Also saves the page each time for troubleshooting.
    public ChallengeSnapshot ReadChallenges()
    {
        _driver.Navigate().GoToUrl(_config.StudentDashboardUrl);
        WaitForDocumentReady();
        SaveSnapshot("wyzwania-latest");

        var raw = ((IJavaScriptExecutor)_driver).ExecuteScript(ParseChallengesScript, Selectors.ChallengeCard.Value)?
            .ToString() ?? "[]";

        var payload = JsonSerializer.Deserialize<List<ChallengePayload>>(raw, _jsonOptions) ?? [];

        var challenges = payload
            .Select(item => new ChallengeInfo(
                TextNormalizer.Normalize(item.Title),
                item.Points,
                item.JoinUrl.Trim(),
                item.Completed,
                TextNormalizer.Normalize(item.Description)))
            .ToList();

        return new ChallengeSnapshot(challenges);
    }

    public void Join(ChallengeInfo challenge)
    {
        if (challenge.JoinUrl.Contains("/student/challenges/", StringComparison.OrdinalIgnoreCase))
        {
            JoinCurrentChallenge(challenge.JoinUrl);
            return;
        }

        _driver.Navigate().GoToUrl(challenge.JoinUrl);
        WaitForDocumentReady();

        // Capture exactly what taking a challenge lands on, so we can confirm
        // whether the click enrolls or just opens a confirmation page.
        SaveSnapshot("challenge-landing");
    }

    private void JoinCurrentChallenge(string joinUrl)
    {
        var result = ((IJavaScriptExecutor)_driver).ExecuteAsyncScript(
            """
            const callback = arguments[arguments.length - 1];
            fetch(arguments[0], {
                method: 'POST',
                credentials: 'same-origin',
                headers: { 'Accept': 'application/json' }
            }).then(response => callback({ ok: response.ok, status: response.status }))
              .catch(error => callback({ ok: false, error: String(error) }));
            """,
            joinUrl) as IDictionary<string, object>;

        if (result is null || !Convert.ToBoolean(result["ok"]))
        {
            var status = result is not null && result.TryGetValue("status", out var statusValue)
                ? $" (HTTP {statusValue})"
                : string.Empty;
            throw new InvalidOperationException($"Could not join the challenge{status}.");
        }

        // Refresh the server-rendered dashboard so subsequent challenge checks
        // observe the new in-progress status.
        _driver.Navigate().GoToUrl(_config.StudentDashboardUrl);
        WaitForDocumentReady();
        SaveSnapshot("challenge-landing");
    }

    private const string ParseChallengesScript =
        """
        const root = document.querySelector(arguments[0]);
        if (root?.id === 'app' && root.dataset.props) {
            try {
                const props = JSON.parse(root.dataset.props);
                const challenges = props.dashboard?.challenges || [];
                return JSON.stringify(challenges.map(challenge => ({
                    title: challenge.title || '',
                    description: challenge.description || '',
                    points: Number(challenge.prize) || 0,
                    joinUrl: challenge.status === 'available'
                        ? `${window.location.origin}/student/challenges/${challenge.id}`
                        : '',
                    completed: challenge.status === 'completed'
                })));
            } catch (_) {
                return '[]';
            }
        }

        const cards = Array.from(document.querySelectorAll(arguments[0]));
        return JSON.stringify(cards.map(card => {
            const text = (card.textContent || '').replace(/\s+/g, ' ').trim();
            const titleEl = card.querySelector('h5');
            const descriptionEl = Array.from(card.querySelectorAll('p'))
                .find(paragraph => /^Opis\s*:/i.test(paragraph.textContent || ''));
            const join = card.querySelector("a[href*='/students/challenge/']");
            const points = text.match(/(?:Nagroda:\s*|)(\d+)\s*pkt/i);
            return {
                title: titleEl ? titleEl.textContent.replace(/\s+/g, ' ').trim() : '',
                description: descriptionEl
                    ? descriptionEl.textContent.replace(/^Opis\s*:\s*/i, '').replace(/\s+/g, ' ').trim()
                    : '',
                points: points ? parseInt(points[1], 10) : 0,
                joinUrl: join ? join.href : '',
                completed: /Gratulacje/i.test(text)
            };
        }));
        """;

    private void SaveSnapshot(string name)
    {
        try
        {
            var dir = Path.Combine(AppContext.BaseDirectory, "diagnostics");
            Directory.CreateDirectory(dir);
            File.WriteAllText(Path.Combine(dir, $"{name}.html"), _driver.PageSource);
        }
        catch
        {
            // Best effort - never let a debug dump break the run.
        }
    }

    private void WaitForDocumentReady()
    {
        new WebDriverWait(new SystemClock(), _driver, _config.DefaultWaitTimeout, _config.PollingInterval)
            .Until(driver => string.Equals(
                ((IJavaScriptExecutor)driver).ExecuteScript("return document.readyState")?.ToString(),
                "complete",
                StringComparison.OrdinalIgnoreCase));
    }

    private sealed class ChallengePayload
    {
        public string Title { get; set; } = string.Empty;

        public string Description { get; set; } = string.Empty;

        public int Points { get; set; }

        public string JoinUrl { get; set; } = string.Empty;

        public bool Completed { get; set; }
    }
}

internal sealed record ChallengeInfo(
    string Title,
    int Points,
    string JoinUrl,
    bool Completed,
    string Description = "")
{
    private const string PerfectionismTitle = "Perfekcjonizm";
    private const string PerfectionismDescription = "Wykonaj 1 lekcję z maksymalnie 1 błędem";

    // A challenge we can still pick has a join link and is not finished.
    public bool IsAvailable => !Completed && !string.IsNullOrWhiteSpace(JoinUrl);

    // In-progress: already taken (no join link) but not yet completed.
    public bool IsActive => !Completed && string.IsNullOrWhiteSpace(JoinUrl);

    // Match the complete title and condition, rather than a title fragment: the
    // other Perfekcjonizm challenges have different lesson counts and must not
    // disable intentional errors accidentally.
    public bool IsPerfectionism => IsActive &&
        string.Equals(Title, PerfectionismTitle, StringComparison.Ordinal) &&
        (string.Equals(Description, PerfectionismDescription, StringComparison.Ordinal) ||
         string.Equals(Description, $"{PerfectionismDescription}.", StringComparison.Ordinal));
}

internal sealed class ChallengeSnapshot (IReadOnlyList<ChallengeInfo> challenges)
{
    public IReadOnlyList<ChallengeInfo> Challenges { get; } = challenges;

    public ChallengeInfo? Active => Challenges.FirstOrDefault(challenge => challenge.IsActive);

    public ChallengeInfo? ActivePerfectionism => Challenges.FirstOrDefault(challenge =>
        challenge.IsActive && challenge.IsPerfectionism);

    public ChallengeInfo? BestAvailable => Challenges
        .Where(challenge => challenge.IsAvailable)
        .OrderByDescending(challenge => challenge.Points)
        .FirstOrDefault();
}
