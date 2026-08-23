using System.Text.Json;
using OpenQA.Selenium;
using OpenQA.Selenium.Support.UI;

namespace LingosBotApp;

internal sealed class ClassRunner(IWebDriver driver, AppConfig config)
{
    private readonly IWebDriver _driver = driver;
    private readonly AppConfig _config = config;
    private readonly JsonSerializerOptions _jsonOptions = new()
    {
        PropertyNameCaseInsensitive = true
    };

    public IReadOnlyList<ClassInfo> ReadClasses()
    {
        _driver.Navigate().GoToUrl(_config.StudentDashboardUrl);
        WaitForDocumentReady();

        var raw = ((IJavaScriptExecutor)_driver).ExecuteScript(
            """
            const select = document.querySelector(arguments[0]);
            if (!select) {
                return '[]';
            }

            return JSON.stringify(Array.from(select.options).map(option => ({
                title: (option.textContent || '').replace(/\s+/g, ' ').trim(),
                changeUrl: new URL(option.value, window.location.href).href
            })).filter(item => item.title && item.changeUrl));
            """,
            Selectors.ClassSelect.Value)?.ToString() ?? "[]";

        var payload = JsonSerializer.Deserialize<List<ClassPayload>>(raw, _jsonOptions) ?? [];
        var classes = payload
            .Where(item => !string.IsNullOrWhiteSpace(item.Title) && !string.IsNullOrWhiteSpace(item.ChangeUrl))
            .Select(item => new ClassInfo(TextNormalizer.Normalize(item.Title), item.ChangeUrl))
            .GroupBy(item => item.ChangeUrl, StringComparer.OrdinalIgnoreCase)
            .Select(group => group.First())
            .ToList();

        if (classes.Count == 0)
        {
            throw new InvalidOperationException(
                "No classes were found in the dashboard's 'Zmień klasę' selector. Verify the class selector in Selectors.cs.");
        }

        return classes;
    }

    public void Select(ClassInfo @class)
    {
        Console.WriteLine($"Selecting class: {@class.Title}");
        _driver.Navigate().GoToUrl(@class.ChangeUrl);
        WaitForDocumentReady();

        var expectedUrl = new Uri(@class.ChangeUrl).AbsoluteUri;
        CreateWait().Until(driver =>
        {
            var selectedUrl = ((IJavaScriptExecutor)driver).ExecuteScript(
                """
                const select = document.querySelector(arguments[0]);
                const option = select?.options[select.selectedIndex];
                return option ? new URL(option.value, window.location.href).href : null;
                """,
                Selectors.ClassSelect.Value)?.ToString();

            return string.Equals(selectedUrl, expectedUrl, StringComparison.OrdinalIgnoreCase);
        });
    }

    private void WaitForDocumentReady()
    {
        CreateWait().Until(driver => string.Equals(
            ((IJavaScriptExecutor)driver).ExecuteScript("return document.readyState")?.ToString(),
            "complete",
            StringComparison.OrdinalIgnoreCase));
    }

    private WebDriverWait CreateWait()
    {
        var wait = new WebDriverWait(new SystemClock(), _driver, _config.DefaultWaitTimeout, _config.PollingInterval);
        wait.IgnoreExceptionTypes(typeof(NoSuchElementException), typeof(StaleElementReferenceException));
        return wait;
    }

    private sealed class ClassPayload
    {
        public string Title { get; set; } = string.Empty;

        public string ChangeUrl { get; set; } = string.Empty;
    }
}

internal sealed record ClassInfo(string Title, string ChangeUrl);
