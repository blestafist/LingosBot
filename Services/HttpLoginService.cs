using System.Net;
using System.Text.RegularExpressions;

namespace LingosBotApp;

// Logs in with plain HTTP, without any browser. Flow: GET /h/login for the CSRF
// token, POST the form, then verify the session on the student dashboard.
// Credentials are never logged.
internal sealed class HttpLoginService : IDisposable
{
    private readonly AppConfig _config;
    private readonly CookieContainer _cookies = new();
    private readonly HttpClient _http;

    public HttpLoginService(AppConfig config) : this(config, null) { }

    internal HttpLoginService(AppConfig config, HttpMessageHandler? handler)
    {
        _config = config;
        handler ??= new HttpClientHandler
        {
            CookieContainer = _cookies,
            AllowAutoRedirect = false,
            AutomaticDecompression = System.Net.DecompressionMethods.All,
        };
        _http = new HttpClient(handler) { Timeout = LingosHttpSession.RequestTimeout };
        _http.DefaultRequestHeaders.UserAgent.ParseAdd(LingosHttpSession.UserAgent);
    }

    // Returns the cookie container to use for the in-memory HTTP session.
    public CookieContainer Login(AppCredentials credentials)
    {
        Console.WriteLine("Logging in over HTTP...");

        var loginUrl = SameOrigin(new Uri(new Uri(_config.BaseUrl), "/h/login"));
        var loginPage = Send(HttpMethod.Get, loginUrl, null);
        var token = ExtractCsrfToken(loginPage)
            ?? throw new LoginFailedException("Login page had no CSRF token. Lingos may have changed the login form.");

        var form = new Dictionary<string, string>
        {
            ["_csrf_token"] = token,
            ["_username"] = credentials.Email,
            ["_password"] = credentials.Password,
        };
        using var post = new HttpRequestMessage(HttpMethod.Post, loginUrl)
        {
            Content = new FormUrlEncodedContent(form),
        };
        using var postResponse = _http.SendAsync(post).GetAwaiter().GetResult();
        var status = (int)postResponse.StatusCode;
        if (status is not (301 or 302 or 303 or 307 or 308) || postResponse.Headers.Location is not { } location)
        {
            throw new LoginFailedException("Login failed. Check credentials.email and credentials.password in config.json.");
        }

        var destination = SameOrigin(new Uri(loginUrl, location));
        if (destination.AbsolutePath.TrimEnd('/').Equals("/h/login", StringComparison.OrdinalIgnoreCase))
            throw new LoginFailedException("Login failed. Check credentials.email and credentials.password in config.json.");

        // Confirm the session by loading the dashboard; a bad login redirects back to the form.
        var dashboardUrl = SameOrigin(new Uri(new Uri(_config.BaseUrl), _config.StudentDashboardUrl));
        var dashboard = Send(HttpMethod.Get, dashboardUrl, null);
        try
        {
            var props = LingosHttpSession.ParsePage(dashboard);
            if (!props.TryGetProperty("page", out var page) || page.GetString() != "dashboard")
                throw new LoginFailedException("Login failed: the authenticated dashboard did not load.");
        }
        catch (Exception ex) when (ex is InvalidOperationException or System.Text.Json.JsonException)
        {
            throw new LoginFailedException("Login failed: the authenticated dashboard did not load.");
        }

        Console.WriteLine("Login succeeded.");
        return _cookies;
    }

    internal static string? ExtractCsrfToken(string html)
    {
        foreach (Match tag in Regex.Matches(html, @"<input\b[^>]*>", RegexOptions.IgnoreCase, TimeSpan.FromSeconds(1)))
        {
            var attributes = Regex.Matches(tag.Value, "([\\w-]+)\\s*=\\s*([\"'])(.*?)\\2",
                RegexOptions.Singleline, TimeSpan.FromSeconds(1));
            var values = attributes.Cast<Match>().ToDictionary(m => m.Groups[1].Value,
                m => WebUtility.HtmlDecode(m.Groups[3].Value), StringComparer.OrdinalIgnoreCase);
            if (values.TryGetValue("name", out var name) && name == "_csrf_token" &&
                values.TryGetValue("value", out var value) && !string.IsNullOrWhiteSpace(value)) return value;
        }
        return null;
    }

    private Uri SameOrigin(Uri target)
    {
        var origin = new Uri(_config.BaseUrl);
        if (target.Scheme != origin.Scheme || target.Authority != origin.Authority || !string.IsNullOrEmpty(target.UserInfo))
            throw new LoginFailedException("Refusing login navigation outside the configured origin.");
        return target;
    }

    private string Send(HttpMethod method, Uri url, HttpContent? content)
    {
        using var request = new HttpRequestMessage(method, url) { Content = content };
        request.Headers.Accept.ParseAdd("text/html");
        using var response = _http.SendAsync(request).GetAwaiter().GetResult();
        var status = (int)response.StatusCode;
        if (status is >= 300 and < 400)
        {
            // Redirects are only expected after a successful POST, so a plain GET
            // should return the page directly. Refuse anything else.
            throw new LoginFailedException("Login page or authenticated dashboard redirected unexpectedly. Check credentials and configured URLs.");
        }

        response.EnsureSuccessStatusCode();
        if (response.Content.Headers.ContentType?.MediaType != "text/html")
            throw new LoginFailedException("Unexpected login response content type.");
        return response.Content.ReadAsStringAsync().GetAwaiter().GetResult();
    }

    public void Dispose() => _http.Dispose();
}
