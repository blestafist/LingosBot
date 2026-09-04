using OpenQA.Selenium;
using OpenQA.Selenium.Support.UI;

namespace LingosBotApp;

internal sealed class LoginService ( IWebDriver driver, AppConfig config )
{
    private readonly IWebDriver _driver = driver;
    private readonly AppConfig _config = config;
    private readonly CookieConsentHandler _cookieConsent = new(driver, config);
    private const int MaxClickAttempts = 3;


    public void Login(AppCredentials credentials)
    {
        Console.WriteLine("Opening lingos.pl...");
        _driver.Navigate().GoToUrl($"{_config.BaseUrl}/h/login");
        WaitForDocumentReady();

        HandleCookieConsent();

        Console.WriteLine("Submitting login form...");

        var emailInput = WaitUntilVisible(Selectors.LoginEmailInput);
        emailInput.Clear();
        emailInput.SendKeys(credentials.Email);

        var passwordInput = WaitUntilVisible(Selectors.LoginPasswordInput);
        passwordInput.Clear();
        passwordInput.SendKeys(credentials.Password);

        var submitButton = WaitUntilClickable(Selectors.LoginSubmitButton);
        ClickElement(submitButton, Selectors.LoginSubmitButton);

        try
        {
            WaitForSuccessfulLogin();
            Console.WriteLine("Login succeeded.");
        }
        catch (WebDriverTimeoutException ex)
        {
            throw new LoginFailedException(
                "Login failed. Check your credentials and update the login selectors in Selectors.cs if the website layout changed.",
                ex);
        }
    }

    private void WaitForSuccessfulLogin()
    {
        var loginPath = "/h/login";

        CreateWait().Until(driver =>
        {
            var currentUrl = driver.Url ?? string.Empty;
            if (!currentUrl.Contains(loginPath, StringComparison.OrdinalIgnoreCase))
            {
                return true;
            }

            if (Selectors.AuthenticatedShellMarker.TryToBy(out var authenticatedBy) &&
                authenticatedBy is not null &&
                TryFindVisible(driver, authenticatedBy, out _))
            {
                return true;
            }

            return false;
        });
    }

    private void HandleCookieConsent()
    {
        Console.WriteLine("Handling cookie consent...");

        try
        {
            if (!_cookieConsent.TryRejectCookies())
            {
                Console.WriteLine("Cookie rejection button was not visible within the short timeout. Continuing.");
                return;
            }

            WaitForDocumentReady();
            Console.WriteLine("Cookie consent rejected.");
        }
        catch (WebDriverTimeoutException)
        {
            Console.WriteLine("Cookie rejection button was not visible within the short timeout. Continuing.");
        }
    }

    private IWebElement WaitUntilVisible(SelectorDefinition selector, TimeSpan? timeout = null)
    {
        var by = selector.ToBy();

        return CreateWait(timeout).Until(driver =>
        {
            try
            {
                var element = driver.FindElement(by);
                return element.Displayed ? element : null;
            }
            catch (NoSuchElementException)
            {
                return null;
            }
            catch (StaleElementReferenceException)
            {
                return null;
            }
        }) ?? throw new WebDriverTimeoutException($"Timed out waiting for selector '{selector.Name}' to become visible.");
    }

    private IWebElement WaitUntilClickable(SelectorDefinition selector, TimeSpan? timeout = null)
    {
        var by = selector.ToBy();

        return CreateWait(timeout).Until(driver =>
        {
            try
            {
                var element = driver.FindElement(by);
                return element.Displayed && element.Enabled ? element : null;
            }
            catch (NoSuchElementException)
            {
                return null;
            }
            catch (StaleElementReferenceException)
            {
                return null;
            }
        }) ?? throw new WebDriverTimeoutException($"Timed out waiting for selector '{selector.Name}' to become clickable.");
    }

    private void WaitForDocumentReady()
    {
        CreateWait().Until(driver =>
        {
            var state = ((IJavaScriptExecutor)driver).ExecuteScript("return document.readyState");
            return string.Equals(state?.ToString(), "complete", StringComparison.OrdinalIgnoreCase);
        });
    }

    private WebDriverWait CreateWait(TimeSpan? timeout = null)
    {
        var wait = new WebDriverWait(new SystemClock(), _driver, timeout ?? _config.DefaultWaitTimeout, _config.PollingInterval);
        wait.IgnoreExceptionTypes(typeof(NoSuchElementException), typeof(StaleElementReferenceException));
        return wait;
    }

    private void ClickElement(IWebElement element, SelectorDefinition selector)
    {
        for (var attempt = 1; attempt <= MaxClickAttempts; attempt++)
        {
            try
            {
                ScrollIntoView(element);
                element.Click();
                return;
            }
            catch (ElementClickInterceptedException)
            {
                if (!_cookieConsent.TryRejectCookies())
                {
                    throw;
                }

                if (attempt == MaxClickAttempts)
                {
                    throw;
                }
            }
            catch (StaleElementReferenceException)
            {
                if (attempt == MaxClickAttempts)
                {
                    throw;
                }
            }

            // Re-fetch after Cookiebot closes, or after a DOM replacement, so
            // the retry cannot use a stale element or the wrong control.
            element = WaitUntilClickable(selector);
        }
    }

    private void ScrollIntoView(IWebElement element)
    {
        ((IJavaScriptExecutor)_driver).ExecuteScript(
            "arguments[0].scrollIntoView({block: 'center', inline: 'center'});",
            element);
    }

    private static bool TryFindVisible(ISearchContext context, By by, out IWebElement? element)
    {
        try
        {
            element = context.FindElement(by);
            return element.Displayed;
        }
        catch (NoSuchElementException)
        {
            element = null;
            return false;
        }
        catch (StaleElementReferenceException)
        {
            element = null;
            return false;
        }
    }
}

internal sealed class LoginFailedException(
    string message,
    Exception? innerException = null)
    : Exception(message, innerException);
