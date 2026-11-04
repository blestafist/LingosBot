using System.Collections.ObjectModel;
using System.Reflection;
using OpenQA.Selenium;
using Xunit;

namespace LingosBotApp.Tests;

public sealed class ClassRunnerTests
{
    [Fact]
    public void SwitchingClassUsesContextApiAndConfirmsReloadedSelection()
    {
        var driver = DispatchProxy.Create<IWebDriver, ClassDriver>();
        var proxy = (ClassDriver)(object)driver;

        new ClassRunner(driver, new AppConfig()).Select(new ClassInfo("German", "https://lingos.pl/student/dashboard", "26083"));

        Assert.Equal("26083", proxy.CurrentGroup);
        Assert.Equal(2, proxy.Navigations);
        Assert.Contains("'/student/context'", proxy.SwitchScript);
        Assert.Contains("props.dashboard?.contexts", proxy.SwitchScript);
        Assert.Contains("{ kind: 'group', id: groupId }", proxy.SwitchScript);
        Assert.Contains("{ groupId }", proxy.SwitchScript); // Older dashboard fallback.
        Assert.Contains("application/json", proxy.SwitchScript);
    }

    [Theory]
    [InlineData("403")]
    [InlineData("error: expected a JSON response from the class switcher")]
    public void FailedSwitchDoesNotReloadAndWaitForAnUnchangedClass(string response)
    {
        var driver = DispatchProxy.Create<IWebDriver, ClassDriver>();
        var proxy = (ClassDriver)(object)driver;
        proxy.Response = response;

        var error = Assert.Throws<InvalidOperationException>(() =>
            new ClassRunner(driver, new AppConfig()).Select(new ClassInfo("German", "https://lingos.pl/student/dashboard", "26083")));

        Assert.Contains(response, error.Message);
        Assert.Equal(1, proxy.Navigations);
    }

    [Fact]
    public void AlreadySelectedClassDoesNotSendASwitchRequest()
    {
        var driver = DispatchProxy.Create<IWebDriver, ClassDriver>();
        var proxy = (ClassDriver)(object)driver;
        proxy.CurrentGroup = "26083";

        new ClassRunner(driver, new AppConfig()).Select(new ClassInfo("German", "https://lingos.pl/student/dashboard", "26083"));

        Assert.Null(proxy.SwitchScript);
        Assert.Equal(1, proxy.Navigations);
    }

    [Fact]
    public void AlreadyOnDashboardDoesNotReloadToSelectTheCurrentClass()
    {
        var driver = DispatchProxy.Create<IWebDriver, ClassDriver>();
        var proxy = (ClassDriver)(object)driver;
        proxy.CurrentGroup = "26083";
        proxy.Url = "https://lingos.pl/student/dashboard";

        new ClassRunner(driver, new AppConfig()).Select(new ClassInfo("German", proxy.Url, "26083"));

        Assert.Null(proxy.SwitchScript);
        Assert.Equal(0, proxy.Navigations);
    }

    public class ClassDriver : DispatchProxy, IJavaScriptExecutor
    {
        public string Url { get; set; } = "about:blank";
        public string CurrentGroup { get; set; } = "25571";
        public string Response { get; set; } = "200";
        public string? SwitchScript { get; private set; }
        public int Navigations { get; private set; }

        public object ExecuteScript(string script, params object?[]? args) =>
            script.Contains("document.readyState") ? "complete" :
            script.Contains("props.dashboard?.currentGroup?.id") ? CurrentGroup :
            throw new InvalidOperationException("Unexpected script: " + script);

        public object ExecuteAsyncScript(string script, params object?[]? args)
        {
            SwitchScript = script;
            if (Response == "200") CurrentGroup = (string)args![0]!;
            return Response;
        }

        public object ExecuteScript(PinnedScript script, params object?[]? args) => throw new NotSupportedException();

        protected override object? Invoke(MethodInfo? method, object?[]? args)
        {
            if (method!.Name == "get_Url") return Url;
            if (method!.Name == "Navigate")
            {
                var navigation = DispatchProxy.Create<INavigation, Stub>();
                ((Stub)(object)navigation).Handler = (_, navigationArgs) =>
                {
                    Url = navigationArgs![0]!.ToString()!;
                    Navigations++;
                    return null;
                };
                return navigation;
            }
            if (method.Name == "FindElements")
            {
                var elements = new List<IWebElement>();
                if (args![0]!.ToString() == Selectors.ClassChangeButton.ToBy().ToString())
                {
                    var button = DispatchProxy.Create<IWebElement, Stub>();
                    ((Stub)(object)button).Handler = (m, _) => m.Name == "get_Displayed" ? true : "listbox";
                    elements.Add(button);
                }
                return new ReadOnlyCollection<IWebElement>(elements);
            }
            throw new NotSupportedException(method.Name);
        }
    }

    public class Stub : DispatchProxy
    {
        public Func<MethodInfo, object?[]?, object?> Handler { get; set; } = null!;
        protected override object? Invoke(MethodInfo? method, object?[]? args) => Handler(method!, args);
    }
}
