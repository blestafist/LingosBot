using System.Net;
using System.Text;
using Xunit;

namespace LingosBotApp.Tests;

public sealed class HttpLoginTests
{
    private static readonly AppCredentials Credentials = new("test@example.org", "test-password");
    private static AppConfig Config() => new() { Credentials = Credentials, DefaultWaitTimeoutSeconds = -1 };
    private const string Form = "<input type='hidden' value='token&amp;value' name='_csrf_token'>";
    private const string Dashboard = "<div id='app' data-props='{&quot;page&quot;:&quot;dashboard&quot;}'></div>";
    private static HttpResponseMessage Html(string text) => new(HttpStatusCode.OK)
        { Content = new StringContent(text, Encoding.UTF8, "text/html") };
    private static HttpResponseMessage Redirect(string? target) => new(HttpStatusCode.Found)
        { Headers = { Location = target is null ? null : new Uri(target, UriKind.RelativeOrAbsolute) } };

    [Theory]
    [InlineData("<input name=\"_csrf_token\" value=\"abc&amp;def\">", "abc&def")]
    [InlineData("<INPUT value='abc' NAME = '_csrf_token'>", "abc")]
    [InlineData("<input name='_csrf_token'><input name='other' value='wrong'>", null)]
    [InlineData("<input name='_csrf_token' value=''>", null)]
    public void TokenParsingHandlesQuotesOrderAndEntities(string html, string? expected)
    {
        Assert.Equal(expected, HttpLoginService.ExtractCsrfToken(html));
    }

    [Theory]
    [InlineData("text/html")]
    [InlineData("Text/HTML")]
    public void LoginPostsFormAndVerifiesAuthenticatedDashboard(string contentType)
    {
        var handler = new StubHandler(async (request, count) =>
        {
            if (count == 1)
            {
                Assert.Equal(HttpMethod.Get, request.Method);
                Assert.Equal("/h/login", request.RequestUri!.AbsolutePath);
                var response = Html(Form);
                response.Content.Headers.ContentType = new(contentType);
                return response;
            }
            if (count == 2)
            {
                Assert.Equal(HttpMethod.Post, request.Method);
                Assert.Equal("application/x-www-form-urlencoded", request.Content!.Headers.ContentType!.MediaType);
                var body = await request.Content.ReadAsStringAsync();
                Assert.Contains("_csrf_token=token%26value", body);
                Assert.Contains("_username=test%40example.org", body);
                Assert.Contains("_password=test-password", body);
                return Redirect("/app/start");
            }
            Assert.Equal("/student/dashboard", request.RequestUri!.AbsolutePath);
            return Html(Dashboard);
        });
        using (var login = new HttpLoginService(Config(), handler))
            Assert.NotNull(login.Login(Credentials));
        Assert.Equal(3, handler.Calls);
        Assert.True(handler.Disposed);
    }

    [Theory]
    [InlineData("/h/login")]
    [InlineData("/h/login?failed=1")]
    [InlineData("https://example.org/steal")]
    [InlineData(null)]
    public void FailedOrForeignLoginRedirectStopsBeforeDashboard(string? target)
    {
        var handler = new StubHandler((_, count) => Task.FromResult(count == 1 ? Html(Form) : Redirect(target)));
        using var login = new HttpLoginService(Config(), handler);
        Assert.Throws<LoginFailedException>(() => login.Login(Credentials));
        Assert.Equal(2, handler.Calls);
    }

    [Theory]
    [InlineData("<div id='app' data-props='{&quot;page&quot;:&quot;login&quot;}'></div>")]
    [InlineData("<div id='app' data-props='{}'></div>")]
    [InlineData("<html>Login</html>")]
    public void AppElementAloneDoesNotConfirmLogin(string dashboard)
    {
        var handler = new StubHandler((_, count) => Task.FromResult(count switch
        {
            1 => Html(Form), 2 => Redirect("/app/start"), _ => Html(dashboard)
        }));
        using var login = new HttpLoginService(Config(), handler);
        Assert.Throws<LoginFailedException>(() => login.Login(Credentials));
        Assert.Equal(3, handler.Calls);
    }

    [Fact]
    public void ForeignDashboardUrlIsNeverRequested()
    {
        var config = Config();
        config.StudentDashboardUrl = "https://example.org/steal";
        var handler = new StubHandler((_, count) => Task.FromResult(count == 1 ? Html(Form) : Redirect("/app/start")));
        using var login = new HttpLoginService(config, handler);
        Assert.Throws<LoginFailedException>(() => login.Login(Credentials));
        Assert.Equal(2, handler.Calls);
    }

    [Fact]
    public void AmbiguousLoginPostIsNotRetried()
    {
        var handler = new StubHandler((_, count) => count == 1
            ? Task.FromResult(Html(Form)) : throw new TaskCanceledException("timeout"));
        using var login = new HttpLoginService(Config(), handler);
        Assert.Throws<TaskCanceledException>(() => login.Login(Credentials));
        Assert.Equal(2, handler.Calls);
    }

    private sealed class StubHandler(Func<HttpRequestMessage, int, Task<HttpResponseMessage>> send) : HttpMessageHandler
    {
        public int Calls { get; private set; }
        public bool Disposed { get; private set; }
        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken) => send(request, ++Calls);
        protected override void Dispose(bool disposing) { Disposed = true; base.Dispose(disposing); }
    }
}
