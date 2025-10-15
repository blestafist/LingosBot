using OpenQA.Selenium;
using SeleniumExtras.WaitHelpers;
using OpenQA.Selenium.Support.UI;
using OpenQA.Selenium.Chrome;
using OpenQA.Selenium.Edge;
using OpenQA.Selenium.Firefox;
using OpenQA.Selenium.Safari;

namespace LingosBot
{
    public static class Helpers
    {
        public static IWebElement WaitForElement(By by, short timeout = 10, Func<IWebDriver, IWebElement>? function = null) // wait for element with set timeout and function (ExpectedConditions)
        {
            WebDriverWait wait = new(Bot.webDriver, TimeSpan.FromSeconds(timeout));
            if (function != null)
            {
                return wait.Until(function);
            }

            return wait.Until(ExpectedConditions.ElementIsVisible(by));
        }

        public static IWebDriver GetWebDriver() // just a switch case expression that returns correct web driver for your browser
        {
            return Enum.Parse<AvailibleDrivers>(Bot.config.browser) switch
            {
                AvailibleDrivers.Chrome => new ChromeDriver(),
                AvailibleDrivers.Edge => new EdgeDriver(),
                AvailibleDrivers.Firefox => new FirefoxDriver(),
                AvailibleDrivers.Safari => new SafariDriver(),
                _ => new ChromeDriver(),
            };
        }
    }
    
    enum AvailibleDrivers : byte // availible drivers (Browsers)
    {
        Firefox,
        Edge,
        Chrome,
        Safari
    }
}