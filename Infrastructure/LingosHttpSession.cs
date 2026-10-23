using System.Net;
using System.Text;
using System.Text.Json;
using System.Text.RegularExpressions;
using OpenQA.Selenium;

namespace LingosBotApp;

// A run-scoped, in-memory cookie jar. No browser processes or JavaScript are
// needed after login. No automatic retries or cross-origin redirects.
internal sealed class LingosHttpSession : ILessonApiClient, IDisposable
{
    internal static readonly JsonSerializerOptions Json = new() { PropertyNameCaseInsensitive = true, PropertyNamingPolicy = JsonNamingPolicy.CamelCase };
    private readonly HttpClient _http;
    private readonly Uri _origin;
    private JsonElement _page;

    internal LingosHttpSession(AppConfig config, HttpMessageHandler handler, string userAgent)
    {
        _origin = new Uri(config.BaseUrl);
        _http = new HttpClient(handler) { Timeout = config.DefaultWaitTimeout };
        _http.DefaultRequestHeaders.UserAgent.ParseAdd(userAgent);
    }

    public static LingosHttpSession FromBrowser(IWebDriver driver, AppConfig config)
    {
        var origin = new Uri(config.BaseUrl);
        if (new Uri(driver.Url).GetLeftPart(UriPartial.Authority) != origin.GetLeftPart(UriPartial.Authority))
            throw new InvalidOperationException("Cannot transfer a browser session from another origin.");
        var cookies = new CookieContainer();
        foreach (var cookie in driver.Manage().Cookies.AllCookies)
        {
            var copy = new System.Net.Cookie(cookie.Name, cookie.Value, cookie.Path, cookie.Domain)
            {
                Secure = cookie.Secure, HttpOnly = cookie.IsHttpOnly
            };
            if (cookie.Expiry is { } expiry) copy.Expires = expiry.ToUniversalTime();
            cookies.Add(copy);
        }
        var userAgent = ((IJavaScriptExecutor)driver).ExecuteScript("return navigator.userAgent;")?.ToString()
            ?? "LingosBot/1.0";
        return new LingosHttpSession(config, new HttpClientHandler
        {
            CookieContainer = cookies, AllowAutoRedirect = false,
            AutomaticDecompression = DecompressionMethods.All
        }, userAgent);
    }

    internal Uri SameOrigin(string url)
    {
        var target = new Uri(_origin, url);
        if (target.Scheme != _origin.Scheme || target.Authority != _origin.Authority || !string.IsNullOrEmpty(target.UserInfo))
            throw new InvalidOperationException("Refusing HTTP navigation outside the configured origin.");
        return target;
    }

    public JsonElement GetPage(string url)
    {
        var target = SameOrigin(url);
        for (var redirect = 0; redirect <= 5; redirect++)
        {
            using var request = new HttpRequestMessage(HttpMethod.Get, target);
            request.Headers.Accept.ParseAdd("text/html");
            using var response = _http.SendAsync(request).GetAwaiter().GetResult();
            if ((int)response.StatusCode is >= 300 and < 400)
            {
                var location = response.Headers.Location ?? throw new InvalidOperationException("HTTP redirect had no destination.");
                target = SameOrigin(new Uri(target, location).AbsoluteUri);
                continue;
            }
            response.EnsureSuccessStatusCode();
            RequireContentType(response, "text/html");
            return _page = ParsePage(response.Content.ReadAsStringAsync().GetAwaiter().GetResult());
        }
        throw new InvalidOperationException("Too many HTTP page redirects. Stopped without retrying.");
    }

    public JsonElement Post(string url, object? body = null)
    {
        using var request = new HttpRequestMessage(HttpMethod.Post, SameOrigin(url));
        request.Headers.Accept.ParseAdd("application/json");
        if (body is not null) request.Content = new StringContent(JsonSerializer.Serialize(body, Json), Encoding.UTF8, "application/json");
        // A timeout or invalid response is ambiguous. Let it stop the run; never
        // replay the POST or fall back to UI input, including lesson entry.
        using var response = _http.SendAsync(request).GetAwaiter().GetResult();
        response.EnsureSuccessStatusCode();
        RequireContentType(response, "application/json");
        using var document = JsonDocument.Parse(response.Content.ReadAsStringAsync().GetAwaiter().GetResult());
        return document.RootElement.Clone();
    }

    public LearningProps? TryRead() => _page.ValueKind == JsonValueKind.Object ? _page.Deserialize<LearningProps>(Json) : null;
    public void Navigate(string url) => GetPage(url);
    public LearningAnswerResult Answer(long lessonId, LearningAnswerRequest request)
    {
        var result = Post($"/learning/answer/{lessonId}", request).Deserialize<LearningAnswerResult>(Json);
        return result?.Step is not null ? result : throw new InvalidOperationException("HTTP lesson answer returned no next step. Stopped without resubmitting.");
    }

    internal static JsonElement ParsePage(string html)
    {
        // Read inert server-rendered JSON, never run downloaded scripts. Match
        // attributes independently so order, quoting and encoded Polish text work.
        var tags = Regex.Matches(html, @"<div\b[^>]*>", RegexOptions.IgnoreCase, TimeSpan.FromSeconds(1));
        foreach (Match tag in tags)
        {
            var attributes = Regex.Matches(tag.Value, "([\\w-]+)\\s*=\\s*([\"'])(.*?)\\2", RegexOptions.Singleline, TimeSpan.FromSeconds(1));
            var values = attributes.Cast<Match>().ToDictionary(m => m.Groups[1].Value, m => WebUtility.HtmlDecode(m.Groups[3].Value), StringComparer.OrdinalIgnoreCase);
            if (!values.TryGetValue("id", out var id) || id != "app" || !values.TryGetValue("data-props", out var props)) continue;
            using var document = JsonDocument.Parse(props);
            if (document.RootElement.ValueKind != JsonValueKind.Object) break;
            return document.RootElement.Clone();
        }
        throw new InvalidOperationException("No server page data. Session may have expired or Lingos changed; HTTP mode stopped safely.");
    }

    private static void RequireContentType(HttpResponseMessage response, string expected)
    {
        if (!string.Equals(response.Content.Headers.ContentType?.MediaType, expected, StringComparison.OrdinalIgnoreCase))
            throw new InvalidOperationException("Unexpected HTTP content type. Stopped without retrying.");
    }

    public void Dispose() => _http.Dispose();
}
