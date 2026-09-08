using OpenQA.Selenium;
using OpenQA.Selenium.Support.UI;

namespace LingosBotApp;

/// <summary>
/// Handles Cookiebot without ever opting in to optional cookies.
/// </summary>
internal sealed class CookieConsentHandler(IWebDriver driver, AppConfig config)
{
    private const int MaxRejectAttempts = 3;
    private readonly IWebDriver _driver = driver;
    private readonly AppConfig _config = config;

    public bool IsRejectButtonVisible()
    {
        try
        {
            return _driver.FindElements(Selectors.CookieRejectButton.ToBy())
                .Any(element => element.Displayed && element.Enabled);
        }
        catch (StaleElementReferenceException)
        {
            return false;
        }
    }

    public bool TryRejectCookies()
    {
        for (var attempt = 1; attempt <= MaxRejectAttempts; attempt++)
        {
            try
            {
                var rejectButton = WaitUntilClickable(Selectors.CookieRejectButton, _config.ShortWaitTimeout);

                try
                {
                    rejectButton.Click();
                }
                catch (ElementClickInterceptedException)
                {
                    // This is the one safe DOM-click fallback: it invokes
                    // Cookiebot's reject action, never an accept/allow action.
                    ((IJavaScriptExecutor)_driver).ExecuteScript("arguments[0].click();", rejectButton);
                }

                WaitForDialogToClose();
                return true;
            }
            catch (StaleElementReferenceException)
            {
                // Cookiebot replaces its banner while it initializes or fades
                // out. Re-locate the decline button, but only a bounded number
                // of times so persistent browser failures are not hidden.
                if (attempt == MaxRejectAttempts)
                {
                    throw;
                }
            }
        }

        return false;
    }

    private IWebElement WaitUntilClickable(SelectorDefinition selector, TimeSpan timeout)
    {
        var by = selector.ToBy();
        var wait = new WebDriverWait(new SystemClock(), _driver, timeout, _config.PollingInterval);
        wait.IgnoreExceptionTypes(typeof(NoSuchElementException), typeof(StaleElementReferenceException));

        return wait.Until(driver => driver.FindElements(by)
            .FirstOrDefault(element => element.Displayed && element.Enabled))
            ?? throw new WebDriverTimeoutException($"Timed out waiting for selector '{selector.Name}' to become clickable.");
    }

    private void WaitForDialogToClose()
    {
        var by = Selectors.CookieRejectButton.ToBy();
        var wait = new WebDriverWait(new SystemClock(), _driver, _config.DefaultWaitTimeout, _config.PollingInterval);
        wait.IgnoreExceptionTypes(typeof(NoSuchElementException), typeof(StaleElementReferenceException));
        wait.Until(driver => driver.FindElements(by).All(element => !element.Displayed));
    }
}
