using System.Net;
using System.Text;
using System.Text.Json;
using Xunit;

namespace LingosBotApp.Tests;

public sealed class HttpSessionTests
{
    private static readonly AppConfig Config = new() { ErrorsPer100Words = 100, LessonCount = 2 };

    private static LingosHttpSession Session(HttpMessageHandler handler, AppConfig? config = null) =>
        new(config ?? Config, handler, "LingosBot.Tests/1.0");

    private static string Html(object props) => $"<html><div data-extra='x' data-props=\"{WebUtility.HtmlEncode(JsonSerializer.Serialize(props))}\" id='app'></div></html>";

    private static HttpResponseMessage Response(string body, string type = "text/html", HttpStatusCode status = HttpStatusCode.OK) =>
        new(status) { Content = new StringContent(body, Encoding.UTF8, type) };

    [Theory]
    [InlineData(false, true, true)]
    [InlineData(true, true, false)]
    [InlineData(false, false, false)]
    [InlineData(true, false, false)]
    public void OnlyVisibleApiRunsSwitchToHttpAfterLogin(bool headless, bool api, bool http)
    {
        var config = new AppConfig { Headless = headless, UseLessonApi = api };
        Assert.Equal(http, config.UseHttpAfterLogin);
    }

    [Theory]
    [InlineData("<div id='app' data-props='{&quot;page&quot;:&quot;dashboard&quot;}'></div>")]
    [InlineData("<DIV data-props=\"{&quot;page&quot;:&quot;dashboard&quot;}\" class='x' id=\"app\"></DIV>")]
    public void ParsesInertPagePropsRegardlessOfAttributeOrderAndQuotes(string html)
    {
        Assert.Equal("dashboard", LingosHttpSession.ParsePage(html).GetProperty("page").GetString());
    }

    [Fact]
    public void DecodesHtmlEntitiesExactlyOnce()
    {
        var props = LingosHttpSession.ParsePage(Html(new { page = "wordset", name = "Łódź &quot; < > ' \"" }));
        Assert.Equal("Łódź &quot; < > ' \"", props.GetProperty("name").GetString());
    }

    [Theory]
    [InlineData("<html><form>Log in</form></html>")]
    [InlineData("<div id='other' data-props='{}'></div>")]
    [InlineData("<div id='app' data-props='null'></div>")]
    public void MissingPayloadStopsInsteadOfGuessing(string html)
    {
        Assert.Throws<InvalidOperationException>(() => LingosHttpSession.ParsePage(html));
    }

    [Fact]
    public void SameOriginGetRedirectsAreFollowedWithoutRendering()
    {
        var handler = new StubHandler((request, count) => count == 1
            ? new HttpResponseMessage(HttpStatusCode.Found) { Headers = { Location = new Uri("/student/dashboard", UriKind.Relative) } }
            : Response(Html(new { page = "dashboard" })));
        using var session = Session(handler);
        Assert.Equal("dashboard", session.GetPage("/start").GetProperty("page").GetString());
        Assert.Equal(2, handler.Calls);
    }

    [Theory]
    [InlineData("https://example.org/steal")]
    [InlineData("http://lingos.pl/student/dashboard")]
    [InlineData("https://lingos.pl:8443/student/dashboard")]
    [InlineData("https://someone@lingos.pl/student/dashboard")]
    public void ForeignOriginIsRejectedBeforeSendingCookies(string url)
    {
        var handler = new StubHandler((_, _) => throw new Exception("Must not send"));
        using var session = Session(handler);
        Assert.Throws<InvalidOperationException>(() => session.GetPage(url));
        Assert.Throws<InvalidOperationException>(() => session.Post(url));
        Assert.Equal(0, handler.Calls);
    }

    [Fact]
    public void ForeignRedirectIsNotFollowed()
    {
        var handler = new StubHandler((_, _) => new HttpResponseMessage(HttpStatusCode.Found)
        { Headers = { Location = new Uri("https://example.org/steal") } });
        using var session = Session(handler);
        Assert.Throws<InvalidOperationException>(() => session.GetPage("/start"));
        Assert.Equal(1, handler.Calls);
    }

    [Fact]
    public void PostRedirectIsNeverFollowedOrRetried()
    {
        var handler = new StubHandler((_, _) => new HttpResponseMessage(HttpStatusCode.TemporaryRedirect)
        { Headers = { Location = new Uri("/answer-again", UriKind.Relative) } });
        using var session = Session(handler);
        Assert.Throws<HttpRequestException>(() => session.Answer(12, new LearningAnswerRequest(1, "house")));
        Assert.Equal(1, handler.Calls);
    }

    [Fact]
    public void AmbiguousPostTimeoutIsNeverRetried()
    {
        var handler = new StubHandler((_, _) => throw new TaskCanceledException("ambiguous timeout"));
        using var session = Session(handler);
        Assert.Throws<TaskCanceledException>(() => session.Answer(12, new LearningAnswerRequest(1, "house")));
        Assert.Equal(1, handler.Calls);
    }

    [Fact]
    public void HtmlLoginResponseToPostIsNotTreatedAsSuccess()
    {
        var handler = new StubHandler((_, _) => Response("<html>Log in</html>"));
        using var session = Session(handler);
        Assert.Throws<InvalidOperationException>(() => session.Answer(12, new LearningAnswerRequest(1, "house")));
        Assert.Equal(1, handler.Calls);
    }

    [Theory]
    [InlineData("paused")]
    [InlineData("")]
    [InlineData("expired")]
    public void UnknownChallengeStatusStopsInsteadOfDisablingPerfectionismProtection(string status)
    {
        var dashboard = JsonSerializer.SerializeToElement(new
        {
            challenges = new[] { new { id = 1, title = "Perfekcjonizm", description = "", prize = 5, status } }
        });
        Assert.Throws<InvalidOperationException>(() => HttpSessionRunner.ParseChallenges(dashboard));
    }

    [Fact]
    public void RunsMultipleClassesAndLessonsWithFreshChallengesAndIntentionalErrors()
    {
        var site = new FakeSite();
        using var session = Session(site);
        var runner = new HttpSessionRunner(session, Config);

        Assert.Equal(4, runner.Run(runner.ReadDashboard()));

        Assert.Equal(4, site.CompletedLessons);
        Assert.Equal(1, site.Switches);
        Assert.Equal(2, site.Joins);
        Assert.Equal(4, site.Starts);
        Assert.Equal(2, site.WrongAnswers); // First lesson in each class is Perfectionism.
        Assert.Equal(10, site.Answers);
        Assert.All(site.MethodsAndPaths, call => Assert.DoesNotContain(".js", call));
        Assert.True(site.DashboardReads >= 8);
    }

    [Fact]
    public void DailyLimitSkipsClassBeforeFetchingVocabularyOrStartingLessons()
    {
        var site = new FakeSite { Limit = true };
        using var session = Session(site);
        var runner = new HttpSessionRunner(session, Config);
        Assert.Equal(0, runner.Run(runner.ReadDashboard()));
        Assert.Equal(0, site.Starts);
        Assert.DoesNotContain(site.MethodsAndPaths, p => p.Contains("wordsets"));
    }

    [Fact]
    public void UnconfirmedJoinStopsBeforeStartingLesson()
    {
        var site = new FakeSite { ConfirmJoin = false };
        using var session = Session(site);
        var runner = new HttpSessionRunner(session, Config);
        Assert.Throws<InvalidOperationException>(() => runner.Run(runner.ReadDashboard()));
        Assert.Equal(1, site.Joins);
        Assert.Equal(0, site.Starts);
    }

    [Fact]
    public void UnconfirmedClassSwitchStopsBeforeStartingWrongClassLesson()
    {
        var site = new FakeSite { ConfirmSwitch = false, Limit = true };
        using var session = Session(site);
        var runner = new HttpSessionRunner(session, Config);
        Assert.Throws<InvalidOperationException>(() => runner.Run(runner.ReadDashboard()));
        Assert.Equal(1, site.Switches);
        Assert.Equal(0, site.Starts);
    }

    [Fact]
    public void IncompleteVocabularyStopsBeforeJoiningOrStarting()
    {
        var site = new FakeSite { ExpectedCards = 3 };
        using var session = Session(site);
        var runner = new HttpSessionRunner(session, Config);
        Assert.Throws<InvalidOperationException>(() => runner.Run(runner.ReadDashboard()));
        Assert.Equal(0, site.Joins);
        Assert.Equal(0, site.Starts);
    }

    [Fact]
    public void SkippedClassesAreNeverSelectedOrStarted()
    {
        var site = new FakeSite();
        var config = new AppConfig { ClassLessonCounts = new() { ["group:1"] = 0, ["group:2"] = 0 } };
        using var session = Session(site, config);
        var runner = new HttpSessionRunner(session, config);
        Assert.Equal(0, runner.Run(runner.ReadDashboard()));
        Assert.Equal(0, site.Switches);
        Assert.Equal(0, site.Starts);
    }

    private sealed class StubHandler(Func<HttpRequestMessage, int, HttpResponseMessage> send) : HttpMessageHandler
    {
        public int Calls { get; private set; }
        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken) => Task.FromResult(send(request, ++Calls));
    }

    private sealed class FakeSite : HttpMessageHandler
    {
        private int _group = 1;
        private int _word = 1;
        private bool _joined;
        private int _lessonsInClass;
        public bool Limit { get; init; }
        public bool ConfirmJoin { get; init; } = true;
        public bool ConfirmSwitch { get; init; } = true;
        public int ExpectedCards { get; init; } = 2;
        public int Starts { get; private set; }
        public int Switches { get; private set; }
        public int Joins { get; private set; }
        public int CompletedLessons { get; private set; }
        public int Answers { get; private set; }
        public int WrongAnswers { get; private set; }
        public int DashboardReads { get; private set; }
        public List<string> MethodsAndPaths { get; } = [];

        private object Step() => new { state = "exercise", exercise = new { identifier = _word, type = _word == 2 ? "new_teacher" : "flashcard", main = "dom" } };
        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            var path = request.RequestUri!.AbsolutePath;
            MethodsAndPaths.Add($"{request.Method} {path}");
            if (path == "/student/dashboard")
            {
                DashboardReads++;
                return Response(Html(new
                {
                    page = "dashboard", dashboard = new
                    {
                        contexts = new { }, currentGroup = new { id = _group },
                        groups = new[] { new { id = 1, name = "Class 1" }, new { id = 2, name = "Class 2" } },
                        lessonLimitReached = Limit,
                        challenges = new[]
                        {
                            new { id = 10, title = "Perfekcjonizm", description = "Wykonaj 1 lekcję bez błędów", prize = 20, status = _lessonsInClass == 0 ? (_joined ? "in_progress" : "available") : "completed" },
                            new { id = 11, title = "Trening", description = "Ćwicz", prize = 5, status = _lessonsInClass == 0 ? "available" : "in_progress" }
                        }
                    }
                }));
            }
            if (path == "/student/context" && request.Method == HttpMethod.Post)
            {
                using var doc = JsonDocument.Parse(await request.Content!.ReadAsStringAsync(cancellationToken));
                Assert.Equal("group", doc.RootElement.GetProperty("kind").GetString());
                if (ConfirmSwitch) _group = doc.RootElement.GetProperty("id").GetInt32();
                Switches++;
                _lessonsInClass = 0;
                _joined = false;
                return Response("{}", "application/json");
            }
            if (path == "/student/wordsets") return Response(Html(new
            {
                page = "wordsets", wordsets = new
                {
                    currentGroup = new { id = _group },
                    wordsets = new[] { new { id = 55, name = "Set", counts = new { total = ExpectedCards } } }
                }
            }));
            if (path == "/student/wordsets/55") return Response(Html(new
            {
                page = "wordset", wordset = new
                {
                    group = new { id = _group }, wordset = new { id = 55 },
                    flashcards = new[] { new { word = "house", translation = "dom" }, new { word = "home", translation = "dom" } }
                }
            }));
            if (path.StartsWith("/student/challenges/") && request.Method == HttpMethod.Post)
            {
                Joins++;
                _joined = ConfirmJoin;
                return Response("{}", "application/json");
            }
            if (path == "/learning/start/0")
            {
                Assert.Equal($"?groupId={_group}", request.RequestUri.Query);
                Starts++;
                _word = 1;
                return Response(Html(new { status = "ok", lessonId = 12, step = Step() }));
            }
            if (path == "/learning/answer/12" && request.Method == HttpMethod.Post)
            {
                Answers++;
                using var body = JsonDocument.Parse(await request.Content!.ReadAsStringAsync(cancellationToken));
                Assert.Equal(_word, body.RootElement.GetProperty("identifier").GetInt32());
                var accepted = _word == 2 || body.RootElement.GetProperty("answer").GetString() == "house";
                if (!accepted) WrongAnswers++;
                if (accepted) _word++;
                if (_word == 3)
                {
                    CompletedLessons++;
                    _lessonsInClass++;
                }
                object step = _word == 3 ? new { state = "finished" } : Step();
                return Response(JsonSerializer.Serialize(new { step, feedback = new { result = accepted ? 1 : 0 } }), "application/json");
            }
            throw new InvalidOperationException($"Unexpected request: {request.Method} {path}");
        }
    }
}
