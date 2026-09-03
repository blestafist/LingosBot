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
        OpenClassSelector();

        var raw = ((IJavaScriptExecutor)_driver).ExecuteScript(
            """
            const select = document.querySelector(arguments[0]);
            if (!select) {
                return '[]';
            }

            return JSON.stringify(Array.from(select.options).map(option => {
                const value = (option.value || '').trim();
                const groupId = /^\d+$/.test(value) ? value : '';
                return {
                    title: (option.textContent || '').replace(/\s+/g, ' ').trim(),
                    groupId,
                    // The legacy page stores a complete group-change URL. The
                    // React dashboard stores only the group ID and changes it
                    // through its Save action instead.
                    changeUrl: groupId ? window.location.href : (value ? new URL(value, window.location.href).href : '')
                };
            }).filter(item => item.title && (item.groupId || item.changeUrl)));
            """,
            Selectors.ClassSelect.Value)?.ToString() ?? "[]";

        var payload = JsonSerializer.Deserialize<List<ClassPayload>>(raw, _jsonOptions) ?? [];
        var classes = payload
            .Where(item => !string.IsNullOrWhiteSpace(item.Title) && !string.IsNullOrWhiteSpace(item.ChangeUrl))
            .Select(item => new ClassInfo(
                TextNormalizer.Normalize(item.Title),
                item.ChangeUrl,
                string.IsNullOrWhiteSpace(item.GroupId) ? null : item.GroupId))
            .GroupBy(item => item.GroupId is not null ? $"group:{item.GroupId}" : $"url:{item.ChangeUrl}", StringComparer.Ordinal)
            .Select(group => group.First())
            .ToList();

        CloseClassSelector();

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

        if (!string.IsNullOrWhiteSpace(@class.GroupId))
        {
            SelectCurrentDashboardClass(@class.GroupId);
            return;
        }

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

    private void SelectCurrentDashboardClass(string groupId)
    {
        // Lesson and vocabulary flows leave the browser on their own pages.
        // The class switcher belongs to the dashboard, so always return there
        // before opening it (including when the requested class is current).
        _driver.Navigate().GoToUrl(_config.StudentDashboardUrl);
        WaitForDocumentReady();
        OpenClassSelector();
        var select = WaitUntilVisible(Selectors.ClassSelect);
        var selectedGroupId = select.GetAttribute("value")?.Trim();

        if (string.Equals(selectedGroupId, groupId, StringComparison.Ordinal))
        {
            CloseClassSelector();
            return;
        }

        new SelectElement(select).SelectByValue(groupId);
        var saveButton = WaitUntilClickable(Selectors.ClassSaveButton);
        saveButton.Click();

        // Saving the new dashboard selection is an asynchronous request. The
        // dialog disappears when it succeeds; waiting for that state prevents
        // the next class from racing the previous update.
        CreateWait().Until(driver => driver.FindElements(Selectors.ClassSelect.ToBy())
            .All(element => !element.Displayed));
    }

    private void CloseClassSelector()
    {
        var closeButton = _driver.FindElements(Selectors.ClassDialogCloseButton.ToBy())
            .FirstOrDefault(element => element.Displayed && element.Enabled);

        if (closeButton is null)
        {
            return;
        }

        try
        {
            closeButton.Click();
        }
        catch (ElementClickInterceptedException)
        {
            // Cookiebot can remain over the page for a short transition after
            // login. The dialog is already identified, so a DOM click is safe
            // and avoids making class discovery depend on that animation.
            ((IJavaScriptExecutor)_driver).ExecuteScript("arguments[0].click();", closeButton);
        }
    }

    private void OpenClassSelector()
    {
        if (_driver.FindElements(Selectors.ClassSelect.ToBy()).Any(element => element.Displayed))
        {
            return;
        }

        var changeButton = WaitUntilClickable(Selectors.ClassChangeButton);
        changeButton.Click();
        WaitUntilVisible(Selectors.ClassSelect);
    }

    private IWebElement WaitUntilVisible(SelectorDefinition selector)
    {
        var by = selector.ToBy();
        return CreateWait().Until(driver => driver.FindElements(by)
            .FirstOrDefault(element => element.Displayed))
            ?? throw new WebDriverTimeoutException($"Timed out waiting for selector '{selector.Name}' to become visible.");
    }

    private IWebElement WaitUntilClickable(SelectorDefinition selector)
    {
        var by = selector.ToBy();
        return CreateWait().Until(driver => driver.FindElements(by)
            .FirstOrDefault(element => element.Displayed && element.Enabled))
            ?? throw new WebDriverTimeoutException($"Timed out waiting for selector '{selector.Name}' to become clickable.");
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

        public string GroupId { get; set; } = string.Empty;
    }
}

internal sealed record ClassInfo(string Title, string ChangeUrl, string? GroupId = null);
