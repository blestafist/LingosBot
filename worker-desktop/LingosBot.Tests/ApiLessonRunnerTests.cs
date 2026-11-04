using System.Reflection;
using System.Text.Json;
using OpenQA.Selenium;
using Xunit;

namespace LingosBotApp.Tests;

public sealed class ApiLessonRunnerTests
{
    private static LearningStep Exercise(long id = 1, string prompt = "dom", string type = "flashcard") => new()
    {
        State = "exercise",
        Exercise = new LearningExercise { Identifier = id, Main = prompt, Type = type }
    };

    private static LearningProps Props(LearningStep step, long lessonId = 12) => new()
    {
        Status = "ok", LessonId = lessonId, Step = step
    };

    private static LearningAnswerResult Result(LearningStep step, int? grade = 1) => new()
    {
        Step = step, Feedback = grade.HasValue ? new LearningFeedback { Result = grade.Value } : null
    };

    private static ApiLessonRunner Runner(FakeClient client, bool errors = false, AppConfig? config = null) => new(
        client, config ?? new AppConfig(),
        prompt => prompt == "dom" ? new[] { "house", "home" } : Array.Empty<string>(),
        () => errors, _ => "wrong");

    [Fact]
    public void LogsWordsSeparatelyFromCallsIncludingRetriesAndNewWordCards()
    {
        var client = new FakeClient();
        client.Results.Enqueue(Result(Exercise(), 0));
        client.Results.Enqueue(Result(Exercise(), 0));
        client.Results.Enqueue(Result(Exercise(2)));
        client.Results.Enqueue(Result(Exercise(3, type: "new_teacher")));
        client.Results.Enqueue(Result(new LearningStep { State = "finished" }, null));
        var messages = new List<string>();
        var errorChecks = 0;
        var runner = new ApiLessonRunner(client, new AppConfig(),
            _ => new[] { "house", "home" }, () => ++errorChecks == 1, _ => "wrong", messages.Add);

        runner.Run(1, Props(Exercise()));

        Assert.StartsWith("[Lesson 1 · Word 1 · Call 1]", messages[0]);
        Assert.StartsWith("[Lesson 1 · Word 1 · Call 2]", messages[1]);
        Assert.Contains(messages, message => message.StartsWith("[Lesson 1 · Word 1 · Call 3]"));
        Assert.Contains(messages, message => message.StartsWith("[Lesson 1 · Word 2 · Call 4]"));
        Assert.Contains(messages, message => message.StartsWith("[Lesson 1 · New word · Call 5]"));
        Assert.Equal("Lesson 1 completed through API after 2 words and 5 calls.", messages[^1]);
    }

    [Fact]
    public void UsesServerStateForIdenticalPromptsAndRefreshesDashboard()
    {
        var client = new FakeClient();
        client.Results.Enqueue(Result(Exercise(2)));
        client.Results.Enqueue(Result(new LearningStep { State = "finished" }));

        Runner(client).Run(1, Props(Exercise()));

        Assert.Equal(new long[] { 1, 2 }, client.Requests.Select(r => r.Request.Identifier));
        Assert.All(client.Requests, r => Assert.Equal("house", r.Request.Answer));
        Assert.Equal("https://lingos.pl/student/dashboard", Assert.Single(client.Navigations));
    }

    [Fact]
    public void RejectionTriesNextCandidateOnlyForTheSameExercise()
    {
        var client = new FakeClient();
        client.Results.Enqueue(Result(Exercise(), 0));
        client.Results.Enqueue(Result(Exercise(2), 0));
        client.Results.Enqueue(Result(new LearningStep { State = "finished" }));

        Runner(client).Run(1, Props(Exercise()));

        Assert.Equal(new[] { "house", "home", "house" }, client.Requests.Select(r => r.Request.Answer));
    }

    [Fact]
    public void IntentionalErrorIsFollowedByCorrectAnswerWithoutConsumingACandidate()
    {
        var client = new FakeClient();
        client.Results.Enqueue(Result(Exercise(), 0));
        client.Results.Enqueue(Result(new LearningStep { State = "finished" }));

        Runner(client, errors: true).Run(1, Props(Exercise()));

        Assert.Equal(new[] { "wrong", "house" }, client.Requests.Select(r => r.Request.Answer));
    }

    [Fact]
    public void DoesNotResubmitOldPromptWhenIntentionalErrorAdvancesServerState()
    {
        var client = new FakeClient();
        client.Results.Enqueue(Result(Exercise(2), 0));
        client.Results.Enqueue(Result(new LearningStep { State = "finished" }, 0));

        Runner(client, errors: true).Run(1, Props(Exercise()));

        Assert.Equal(new long[] { 1, 2 }, client.Requests.Select(r => r.Request.Identifier));
    }

    [Theory]
    [InlineData("new_teacher")]
    [InlineData("new_premium")]
    public void NewWordsAreAcknowledgedWithoutGuessingOrInjectingErrors(string type)
    {
        var client = new FakeClient();
        client.Results.Enqueue(Result(new LearningStep { State = "finished" }, null));

        Runner(client, errors: true).Run(1, Props(Exercise(type: type)));

        Assert.Equal(string.Empty, Assert.Single(client.Requests).Request.Answer);
    }

    [Fact]
    public void WordsetTransitionReadsFreshPayloadAndUsesNewLessonId()
    {
        var client = new FakeClient { NextProps = Props(Exercise(2), 99) };
        client.Results.Enqueue(Result(new LearningStep { State = "change_wordset", ContinueUrl = "/learning/next" }));
        client.Results.Enqueue(Result(new LearningStep { State = "finished" }));

        Runner(client).Run(1, Props(Exercise()));

        Assert.Equal(new long[] { 12, 99 }, client.Requests.Select(r => r.LessonId));
        Assert.Equal("/learning/next", client.Navigations[0]);
    }

    [Fact]
    public void DailyLimitDuringTransitionStopsCleanly()
    {
        var client = new FakeClient { NextProps = new LearningProps { Status = "error", Reason = "limit-exceeded" } };
        var initial = Props(new LearningStep { State = "change_wordset", ContinueUrl = "/learning/next" });

        Assert.Throws<LessonLimitReachedException>(() => Runner(client).Run(1, initial));
        Assert.Empty(client.Requests);
    }

    [Theory]
    [InlineData("exercise", "unknown", "dom")]
    [InlineData("exercise", "flashcard", "unknown")]
    [InlineData("exercise", "flashcard", " ")]
    [InlineData("unknown", "flashcard", "dom")]
    [InlineData("select_level", "flashcard", "dom")]
    public void UnknownDataStopsBeforeAnySubmission(string state, string type, string prompt)
    {
        var client = new FakeClient();
        var step = Exercise(prompt: prompt, type: type);
        step.State = state;

        Assert.Throws<InvalidOperationException>(() => Runner(client).Run(1, Props(step)));
        Assert.Empty(client.Requests);
    }

    [Fact]
    public void ExhaustedCandidatesStopWithoutFurtherSubmissions()
    {
        var client = new FakeClient();
        client.Results.Enqueue(Result(Exercise(), 0));
        client.Results.Enqueue(Result(Exercise(), 0));

        Assert.Throws<InvalidOperationException>(() => Runner(client).Run(1, Props(Exercise())));
        Assert.Equal(2, client.Requests.Count);
    }

    [Fact]
    public void SubmissionFailureIsNeverRetriedOrFollowedByNavigation()
    {
        var client = new FakeClient { Fail = true };

        Assert.Throws<WebDriverTimeoutException>(() => Runner(client).Run(1, Props(Exercise())));
        Assert.Single(client.Requests);
        Assert.Empty(client.Navigations);
    }

    [Fact]
    public void MissingFlashcardFeedbackStopsWithoutResubmitting()
    {
        var client = new FakeClient();
        client.Results.Enqueue(Result(Exercise(), null));

        Assert.Throws<InvalidOperationException>(() => Runner(client).Run(1, Props(Exercise())));
        Assert.Single(client.Requests);
    }

    [Fact]
    public void SafetyCapAllowsFinalCompletionButNotAnotherPrompt()
    {
        var config = new AppConfig { LessonPromptSafetyCap = 1 };
        var complete = new FakeClient();
        complete.Results.Enqueue(Result(new LearningStep { State = "finished" }));
        Runner(complete, config: config).Run(1, Props(Exercise()));
        var looping = new FakeClient();
        looping.Results.Enqueue(Result(Exercise(2)));

        Assert.Throws<InvalidOperationException>(() => Runner(looping, config: config).Run(1, Props(Exercise())));
        Assert.Single(looping.Requests);
    }

    [Theory]
    [InlineData("null")]
    [InlineData("not json")]
    [InlineData("{\"ok\":false,\"status\":429}")]
    [InlineData("{\"ok\":true,\"status\":200,\"redirected\":true,\"body\":\"{}\"}")]
    [InlineData("{\"ok\":true,\"status\":200,\"body\":\"<html>login</html>\"}")]
    [InlineData("{\"ok\":true,\"status\":200,\"body\":\"{}\"}")]
    public void InvalidHttpResponsesStopWithoutRetry(string raw)
    {
        Assert.Throws<InvalidOperationException>(() => LessonApiClient.ParseResponse(raw));
    }

    [Fact]
    public void ParsesServerResponse()
    {
        var raw = JsonSerializer.Serialize(new { ok = true, status = 200, body = "{\"step\":{\"state\":\"finished\"}}" });
        Assert.Equal("finished", LessonApiClient.ParseResponse(raw).Step!.State);
    }

    [Fact]
    public void MissingInitialPayloadAllowsUiFallback()
    {
        var driver = DispatchProxy.Create<IWebDriver, ApiDriver>();
        var proxy = (ApiDriver)(object)driver;
        var api = new LessonApiClient(driver, new AppConfig());

        Assert.Null(api.TryRead());
        proxy.Props = "invalid json";
        Assert.Null(api.TryRead());
        proxy.Props = "{\"status\":\"ok\",\"lessonId\":42,\"step\":{\"state\":\"exercise\"}}";
        Assert.Equal(42, api.TryRead()!.LessonId);
        Assert.Equal(0, proxy.Posts);
    }

    [Fact]
    public void ClientUsesOnePostWithCamelCaseBodyAndRejectsForeignOrigin()
    {
        var driver = DispatchProxy.Create<IWebDriver, ApiDriver>();
        var proxy = (ApiDriver)(object)driver;
        var api = new LessonApiClient(driver, new AppConfig());

        api.Answer(42, new LearningAnswerRequest(17, "house"));

        Assert.Equal(1, proxy.Posts);
        Assert.Equal("https://lingos.pl/learning/answer/42", proxy.Arguments![0]);
        using var body = JsonDocument.Parse((string)proxy.Arguments[1]!);
        Assert.Equal(17, body.RootElement.GetProperty("identifier").GetInt64());
        Assert.Equal("house", body.RootElement.GetProperty("answer").GetString());
        Assert.False(body.RootElement.GetProperty("skip").GetBoolean());
        Assert.Throws<InvalidOperationException>(() => api.Navigate("https://example.com/learning/next"));
        proxy.Url = "https://example.com/";
        Assert.Throws<InvalidOperationException>(() => api.Answer(42, new LearningAnswerRequest(17, "house")));
        Assert.Equal(1, proxy.Posts);
    }

    private sealed class FakeClient : ILessonApiClient
    {
        public Queue<LearningAnswerResult> Results { get; } = new();
        public List<(long LessonId, LearningAnswerRequest Request)> Requests { get; } = [];
        public List<string> Navigations { get; } = [];
        public LearningProps? NextProps { get; init; }
        public bool Fail { get; init; }
        public LearningProps? TryRead() => NextProps;
        public void Navigate(string url) => Navigations.Add(url);
        public LearningAnswerResult Answer(long lessonId, LearningAnswerRequest request)
        {
            Requests.Add((lessonId, request));
            if (Fail) throw new WebDriverTimeoutException("ambiguous submission timeout");
            return Results.Dequeue();
        }
    }

    public class ApiDriver : DispatchProxy, IJavaScriptExecutor
    {
        public string Url { get; set; } = "https://lingos.pl/learning/start/";
        public string? Props { get; set; }
        public int Posts { get; private set; }
        public object?[]? Arguments { get; private set; }
        public object ExecuteScript(string script, params object?[]? args) => Props!;
        public object ExecuteScript(PinnedScript script, params object?[]? args) => throw new NotSupportedException();
        public object ExecuteAsyncScript(string script, params object?[]? args)
        {
            Posts++;
            Arguments = args;
            return JsonSerializer.Serialize(new { ok = true, status = 200, body = "{\"step\":{\"state\":\"finished\"}}" });
        }
        protected override object? Invoke(MethodInfo? targetMethod, object?[]? args) =>
            targetMethod?.Name == "get_Url" ? Url : throw new InvalidOperationException("Unexpected browser command.");
    }
}
