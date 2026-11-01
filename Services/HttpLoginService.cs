using System.Net;
using System.Text.RegularExpressions;

namespace LingosBotApp;

// Logs in with plain HTTP, without any browser. Flow: GET /h/login for the CSRF
// token, POST the form, then verify the session on the student dashboard.
// Credentials are never logged.
internal sealed class HttpLoginService
{
    private static readonly Regex CsrfTokenPattern = new(
        "name=\"_csrf_token\"[^>]*value=\"([^\"]+)\"|value=\"([^\"]+)\"[^>]*name=\"_csrf_token\"",
        RegexOptions.IgnoreCase | RegexOptions.Singleline,
        TimeSpan.FromSeconds(1));

    private readonly AppConfig _config;
    private readonly CookieContainer _cookies = new();
    private readonly HttpClient _http;

    public HttpLoginService(AppConfig config)
    {
        _config = config;
        var handler = new HttpClientHandler
        {
            CookieContainer = _cookies,
            AllowAutoRedirect = false,
            AutomaticDecompression = System.Net.DecompressionMethods.All,
        };
        _http = new HttpClient(handler) { Timeout = config.DefaultWaitTimeout };
        _http.DefaultRequestHeaders.UserAgent.ParseAdd("Mozilla/5.0 (X11; Linux x86_64) LingosBot/1.0");
    }

    // Returns the cookie container to use for the in-memory HTTP session.
    public CookieContainer Login(AppCredentials credentials)
    {
        Console.WriteLine("Logging in over HTTP...");

        var loginUrl = new Uri(new Uri(_config.BaseUrl), "/h/login");
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
        if (status is < 300 or >= 400)
        {
            throw new LoginFailedException("Login failed. Check credentials.email and credentials.password in config.json.");
        }

        // Confirm the session by loading the dashboard; a bad login redirects back to the form.
        var dashboardUrl = new Uri(new Uri(_config.BaseUrl), _config.StudentDashboardUrl);
        var dashboard = Send(HttpMethod.Get, dashboardUrl, null);
        if (!dashboard.Contains("id=\"app\"", StringComparison.OrdinalIgnoreCase) ||
            !dashboard.Contains("data-props", StringComparison.OrdinalIgnoreCase))
        {
            throw new LoginFailedException("Login failed: the dashboard did not load. Check credentials in config.json.");
        }

        Console.WriteLine("Login succeeded.");
        return _cookies;
    }

    internal static string? ExtractCsrfToken(string html)
    {
        var match = CsrfTokenPattern.Match(html);
        if (!match.Success)
        {
            return null;
        }

        var value = match.Groups[1].Success ? match.Groups[1].Value : match.Groups[2].Value;
        return WebUtility.HtmlDecode(value);
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
            throw new LoginFailedException("Unexpected redirect while loading the login page.");
        }

        response.EnsureSuccessStatusCode();
        return response.Content.ReadAsStringAsync().GetAwaiter().GetResult();
    }
}
