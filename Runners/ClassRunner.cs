using System.Text.Json;
using OpenQA.Selenium;
using OpenQA.Selenium.Support.UI;

namespace LingosBotApp;

internal sealed class ClassRunner(IWebDriver driver, AppConfig config)
{
    private readonly IWebDriver _driver = driver;
    private readonly AppConfig _config = config;
    private readonly CookieConsentHandler _cookieConsent = new(driver, config);
    private const int MaxInteractionAttempts = 3;
    private readonly JsonSerializerOptions _jsonOptions = new()
    {
        PropertyNameCaseInsensitive = true
    };

    public IReadOnlyList<ClassInfo> ReadClasses()
    {
        _driver.Navigate().GoToUrl(_config.StudentDashboardUrl);
        WaitForDocumentReady();
        RejectCookiesAfterNavigation();

        var raw = ReadDashboardClassPayload();
        var classes = ParseClasses(raw);

        if (classes.Count == 0)
        {
            OpenClassSelector();
            raw = ReadClassSelectorPayload();
            classes = ParseClasses(raw);
            CloseClassSelector();
        }

        if (classes.Count == 0)
        {
            throw new InvalidOperationException(
                "No classes were found in the dashboard data or its 'Zmień klasę' selector. Verify the dashboard response and class selectors.");
        }

        return classes;
    }

    private List<ClassInfo> ParseClasses(string raw)
    {
        var payload = JsonSerializer.Deserialize<List<ClassPayload>>(raw, _jsonOptions) ?? [];
        return payload
            .Where(item => !string.IsNullOrWhiteSpace(item.Title) && !string.IsNullOrWhiteSpace(item.ChangeUrl))
            .Select(item => new ClassInfo(
                TextNormalizer.Normalize(item.Title),
                item.ChangeUrl,
                string.IsNullOrWhiteSpace(item.GroupId) ? null : item.GroupId))
            .GroupBy(item => item.GroupId is not null ? $"group:{item.GroupId}" : $"url:{item.ChangeUrl}", StringComparer.Ordinal)
            .Select(group => group.First())
            .ToList();
    }

    private string ReadDashboardClassPayload()
    {
        return ((IJavaScriptExecutor)_driver).ExecuteScript(
            """
            try {
                const root = document.querySelector('#app[data-props]');
                const props = JSON.parse(root?.getAttribute('data-props') || '{}');
                return JSON.stringify((props.dashboard?.groups || []).map(group => ({
                    title: String(group.name || '').replace(/\s+/g, ' ').trim(),
                    groupId: String(group.id || ''),
                    changeUrl: window.location.href
                })));
            } catch {
                return '[]';
            }
            """)?.ToString() ?? "[]";
    }

    private string ReadClassSelectorPayload()
    {
        return ((IJavaScriptExecutor)_driver).ExecuteScript(
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

    public bool IsLessonLimitReached() => LessonLimitDetector.IsReached(_driver.PageSource);

    private void SelectCurrentDashboardClass(string groupId)
    {
        // Lesson and vocabulary flows leave the browser on their own pages.
        // The class switcher belongs to the dashboard, so always return there
        // before opening it (including when the requested class is current).
        _driver.Navigate().GoToUrl(_config.StudentDashboardUrl);
        WaitForDocumentReady();
        RejectCookiesAfterNavigation();
        OpenClassSelector();
        var select = WaitUntilVisible(Selectors.ClassSelect);
        var selectedGroupId = select.GetAttribute("value")?.Trim();

        if (string.Equals(selectedGroupId, groupId, StringComparison.Ordinal))
        {
            CloseClassSelector();
            return;
        }

        SelectClass(select, groupId);
        var saveButton = WaitUntilClickable(Selectors.ClassSaveButton);
        ClickElement(saveButton, Selectors.ClassSaveButton);

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

        ClickElement(closeButton, Selectors.ClassDialogCloseButton);
    }

    private void OpenClassSelector()
    {
        if (_driver.FindElements(Selectors.ClassSelect.ToBy()).Any(element => element.Displayed))
        {
            return;
        }

        ClickElement(WaitUntilClickable(Selectors.ClassChangeButton), Selectors.ClassChangeButton);
        WaitUntilVisible(Selectors.ClassSelect);
    }

    private void RejectCookiesAfterNavigation()
    {
        if (!_cookieConsent.IsRejectButtonVisible())
        {
            return;
        }

        if (_cookieConsent.TryRejectCookies())
        {
            Console.WriteLine("Cookie consent rejected after dashboard navigation.");
        }
    }

    private void SelectClass(IWebElement select, string groupId)
    {
        for (var attempt = 1; attempt <= MaxInteractionAttempts; attempt++)
        {
            try
            {
                new SelectElement(select).SelectByValue(groupId);
                return;
            }
            catch (ElementClickInterceptedException)
            {
                if (!_cookieConsent.TryRejectCookies())
                {
                    throw;
                }

                if (attempt == MaxInteractionAttempts)
                {
                    throw;
                }
            }
            catch (StaleElementReferenceException)
            {
                if (attempt == MaxInteractionAttempts)
                {
                    throw;
                }
            }

            // SelectByValue performs a native click in Selenium. Re-fetch the
            // select after Cookiebot closes or after a DOM replacement.
            select = WaitUntilVisible(Selectors.ClassSelect);
        }
    }

    private void ClickElement(IWebElement element, SelectorDefinition selector)
    {
        for (var attempt = 1; attempt <= MaxInteractionAttempts; attempt++)
        {
            try
            {
                element.Click();
                return;
            }
            catch (ElementClickInterceptedException)
            {
                if (!_cookieConsent.TryRejectCookies())
                {
                    throw;
                }

                if (attempt == MaxInteractionAttempts)
                {
                    throw;
                }
            }
            catch (StaleElementReferenceException)
            {
                if (attempt == MaxInteractionAttempts)
                {
                    throw;
                }
            }

            element = WaitUntilClickable(selector);
        }
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
