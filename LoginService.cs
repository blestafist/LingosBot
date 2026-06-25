using OpenQA.Selenium;
using OpenQA.Selenium.Support.UI;
using SeleniumExtras.WaitHelpers;

namespace LingosBot;

internal sealed class LoginService
{
    private readonly IWebDriver _driver;
    private readonly AppConfig _config;

    public LoginService(IWebDriver driver, AppConfig config)
    {
        _driver = driver;
        _config = config;
    }

    public void Login()
    {
        if (!_config.AutomaticLogin)
        {
            Console.WriteLine("Login to Lingos and press enter...");
            Console.ReadLine();
            return;
        }

        try
        {
            Console.WriteLine("Logging in...");

            // Decline cookies
            var declineCookies = WaitForElement(
                Selectors.CookieDeclineButton.ToBy(),
                15,
                ExpectedConditions.ElementToBeClickable(Selectors.CookieDeclineButton.ToBy()));
            declineCookies.Click();

            // Find login elements
            var loginBox = WaitForElement(
                Selectors.LoginEmailInput.ToBy(),
                10,
                ExpectedConditions.ElementToBeClickable(Selectors.LoginEmailInput.ToBy()));
            var passwordBox = _driver.FindElement(Selectors.LoginPasswordInput.ToBy());
            var submitButton = _driver.FindElement(Selectors.LoginSubmitButton.ToBy());

            // Enter credentials
            loginBox.SendKeys(_config.Email);
            passwordBox.SendKeys(_config.Password);

            // Submit with JavaScript to ensure clickability
            ((IJavaScriptExecutor)_driver).ExecuteScript("arguments[0].click();", submitButton);

            Console.WriteLine("Login successful.");
        }
        catch (Exception e)
        {
            Console.WriteLine("Error while logging in: " + e.Message);
            throw;
        }
    }

    private IWebElement WaitForElement(By by, int timeoutSeconds = 10, Func<IWebDriver, IWebElement>? condition = null)
    {
        var wait = new WebDriverWait(_driver, TimeSpan.FromSeconds(timeoutSeconds));

        if (condition != null)
        {
            return wait.Until(condition);
        }

        return wait.Until(ExpectedConditions.ElementIsVisible(by));
    }
}
