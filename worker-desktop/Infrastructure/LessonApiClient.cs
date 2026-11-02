using System.Text.Json;
using OpenQA.Selenium;

namespace LingosBotApp;

internal interface ILessonApiClient
{
    LearningProps? TryRead();
    LearningAnswerResult Answer(long lessonId, LearningAnswerRequest request);
    void Navigate(string url);
}

// Use the browser's existing authenticated session, without exporting cookies.
internal sealed class LessonApiClient(IWebDriver driver, AppConfig config) : ILessonApiClient
{
    private static readonly JsonSerializerOptions Json = new()
    {
        PropertyNameCaseInsensitive = true,
        PropertyNamingPolicy = JsonNamingPolicy.CamelCase
    };

    public LearningProps? TryRead()
    {
        if (driver is not IJavaScriptExecutor executor) return null;
        try
        {
            // Only used immediately after entering/navigating to a lesson.
            // data-props is initial server state, not the live React state.
            var raw = executor.ExecuteScript(
                "return document.querySelector('#app[data-props]')?.getAttribute('data-props') || null;") as string;
            var props = raw is null ? null : JsonSerializer.Deserialize<LearningProps>(raw, Json);
            return props is { Status: "ok", LessonId: > 0, Step: not null } or
                { Status: "error", Reason.Length: > 0 } ? props : null;
        }
        catch (Exception ex) when (ex is WebDriverException or JsonException)
        {
            return null;
        }
    }

    public LearningAnswerResult Answer(long lessonId, LearningAnswerRequest request)
    {
        var url = SameOriginUrl($"/learning/answer/{lessonId}");
        // Never retry a POST, even on timeout: the server may already have
        // advanced the lesson. Never fall back to a stale DOM after a POST.
        var raw = ((IJavaScriptExecutor)driver).ExecuteAsyncScript(
            PostScript, url, JsonSerializer.Serialize(request, Json),
            (long)config.DefaultWaitTimeout.TotalMilliseconds)?.ToString();
        return ParseResponse(raw);
    }

    internal static LearningAnswerResult ParseResponse(string? raw)
    {
        try
        {
            var response = JsonSerializer.Deserialize<HttpResult>(raw ?? "null", Json);
            if (response is not { Ok: true, Redirected: false, Status: >= 200 and < 300 })
            {
                throw new InvalidOperationException(
                    $"Lesson API request failed (HTTP {response?.Status ?? 0}, redirected: {response?.Redirected ?? false}). " +
                    "Stopped without retrying; the answer may already have been processed.");
            }

            var result = JsonSerializer.Deserialize<LearningAnswerResult>(response.Body, Json);
            return result?.Step is not null ? result : throw new InvalidOperationException(
                "Lesson API returned no next step. Stopped without resubmitting the answer.");
        }
        catch (JsonException ex)
        {
            throw new InvalidOperationException(
                "Lesson API returned unrecognized data. Stopped without resubmitting the answer.", ex);
        }
    }

    public void Navigate(string url) => driver.Navigate().GoToUrl(SameOriginUrl(url));

    private string SameOriginUrl(string url)
    {
        var origin = new Uri(config.BaseUrl);
        var target = new Uri(origin, url);
        var current = new Uri(driver.Url);
        if (target.Scheme != origin.Scheme || target.Authority != origin.Authority ||
            current.Scheme != origin.Scheme || current.Authority != origin.Authority)
        {
            throw new InvalidOperationException("Refusing a lesson API request or navigation outside the configured origin.");
        }
        return target.AbsoluteUri;
    }

    private sealed class HttpResult
    {
        public bool Ok { get; set; }
        public int Status { get; set; }
        public bool Redirected { get; set; }
        public string Body { get; set; } = string.Empty;
    }

    internal const string PostScript = """
        const [url, body, timeout] = arguments;
        const done = arguments[arguments.length - 1];
        if (new URL(url, location.href).origin !== location.origin) {
            done(JSON.stringify({ok: false, status: 0}));
            return;
        }
        fetch(url, {
            method: 'POST', credentials: 'same-origin', redirect: 'error',
            headers: {'Accept': 'application/json', 'Content-Type': 'application/json'},
            body, signal: AbortSignal.timeout(timeout)
        }).then(async response => done(JSON.stringify({
            ok: response.ok, status: response.status, redirected: response.redirected,
            body: await response.text()
        }))).catch(() => done(JSON.stringify({ok: false, status: 0})));
        """;
}
