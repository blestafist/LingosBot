using System.Reflection;
using OpenQA.Selenium;
using Xunit;

namespace LingosBotApp.Tests;

public sealed class LessonPageReaderTests
{
    [Theory]
    [InlineData(true, true, true, true, true, "Finished")]
    [InlineData(false, true, true, true, true, "Blocked")]
    [InlineData(false, false, true, true, true, "Prompt")]
    [InlineData(false, false, true, true, false, "Prompt")]
    [InlineData(false, false, true, false, true, "ContinueOnly")]
    [InlineData(false, false, false, false, true, "ContinueOnly")]
    [InlineData(false, false, false, true, true, "Waiting")]
    [InlineData(false, false, true, false, false, "Waiting")]
    [InlineData(false, false, false, false, false, "Waiting")]
    public void SnapshotPreservesStepPriority(
        bool finished, bool blocked, bool prompt, bool input, bool continueButton, string expected)
    {
        var element = DispatchProxy.Create<IWebElement, UnusedElement>();
        var snapshot = new LessonPageSnapshot(
            finished, blocked, prompt ? element : null, input, continueButton, false, false);

        var step = LessonRunner.ReadStep(snapshot);

        Assert.Equal(expected, step.Kind.ToString());
        Assert.Same(step.Kind == LessonStepKind.Prompt ? element : null, step.PromptElement);
    }

    [Fact]
    public void SnapshotUsesOneCommandAndDoesNotCacheAcrossReads()
    {
        var driver = DispatchProxy.Create<IWebDriver, ScriptDriver>();
        var proxy = (ScriptDriver)(object)driver;
        var reader = new LessonPageReader(driver);

        Assert.False(reader.TryRead()!.Finished);
        proxy.Finished = true;
        Assert.True(reader.TryRead()!.Finished);
        Assert.Equal(2, proxy.Calls);
    }

    [Fact]
    public void UnsupportedScriptAndNavigationErrorsAllowSeleniumFallback()
    {
        var driver = DispatchProxy.Create<IWebDriver, ScriptDriver>();
        var proxy = (ScriptDriver)(object)driver;
        var reader = new LessonPageReader(driver);

        proxy.Unsupported = true;
        Assert.Null(reader.TryRead());
        proxy.Fail = true;
        Assert.Null(reader.TryRead());
    }

    public class UnusedElement : DispatchProxy
    {
        protected override object? Invoke(MethodInfo? targetMethod, object?[]? args) =>
            throw new InvalidOperationException("Snapshot classification must not query the browser.");
    }

    public class ScriptDriver : DispatchProxy, IJavaScriptExecutor
    {
        public int Calls { get; private set; }
        public bool Finished { get; set; }
        public bool Unsupported { get; set; }
        public bool Fail { get; set; }

        public object ExecuteScript(string script, params object?[]? args)
        {
            Calls++;
            if (Fail) throw new JavaScriptException("Document replaced");
            if (Unsupported) return null!;
            return new Dictionary<string, object>
            {
                ["finished"] = Finished,
                ["blocked"] = false,
                ["prompt"] = null!,
                ["input"] = false,
                ["continueButton"] = false,
                ["feedback"] = false,
                ["incorrect"] = false
            };
        }

        public object ExecuteScript(PinnedScript script, params object?[]? args) =>
            throw new NotSupportedException();

        public object ExecuteAsyncScript(string script, params object?[]? args) =>
            throw new NotSupportedException();

        protected override object? Invoke(MethodInfo? targetMethod, object?[]? args) =>
            throw new InvalidOperationException("The fast reader must use only one script command.");
    }
}
