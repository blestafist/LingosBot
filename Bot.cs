using OpenQA.Selenium;
using OpenQA.Selenium.Support.UI;
using SeleniumExtras.WaitHelpers;


using OpenQA.Selenium.Chrome;
using OpenQA.Selenium.Edge;
using OpenQA.Selenium.Firefox;
using OpenQA.Selenium.Safari;



namespace LingosBot
{

    internal class Bot
    {
        public static Config config = ConfigDataBaseTweaks.GetConfig(); // getting user config
        public static IWebDriver webDriver = Helpers.GetWebDriver();
        public static void Main(string[] args)
        {
            Console.WriteLine(config.email);

            Console.WriteLine("Entry point");
        }
    }

    public static class Helpers
    {
        public static IWebElement WaitForElement(By by, short timeout = 10, Func<IWebDriver, IWebElement>? function = null)
        {
            WebDriverWait wait = new(Bot.webDriver, TimeSpan.FromSeconds(timeout));
            if (function != null)
            {
                return wait.Until(function);
            }

            return wait.Until(ExpectedConditions.ElementIsVisible(by));
        }
        
        public static IWebDriver GetWebDriver()
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


    enum AvailibleDrivers : byte
    {
        Firefox,
        Edge,
        Chrome,
        Safari
    }
}