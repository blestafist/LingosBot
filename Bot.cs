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
            webDriver.Navigate().GoToUrl("https://lingos.pl/h/login");
            SeleniumMethods.Login();  // logging in

            Console.ReadLine();
        }
    }

    internal static class SeleniumMethods
    {
        public static void Login()
        {
            if (Bot.config.automaticLogin)
            {
                try
                {
                    string login = Bot.config.email;
                    string password = Bot.config.password;

                    var declineCookies = Helpers.WaitForElement(By.Id("CybotCookiebotDialogBodyButtonDecline"), 15, ExpectedConditions.ElementToBeClickable(By.Id("CybotCookiebotDialogBodyButtonDecline")));
                    declineCookies.Click();

                    var loginBox = Helpers.WaitForElement(By.Name("login"), 10, ExpectedConditions.ElementToBeClickable(By.Name("login")));
                    var passwordBox = Bot.webDriver.FindElement(By.Name("password"));
                    var submitButton = Bot.webDriver.FindElement(By.Id("submit-login-button"));

                    loginBox.SendKeys(login);
                    passwordBox.SendKeys(password);
                    ((IJavaScriptExecutor)Bot.webDriver).ExecuteScript("arguments[0].click();", submitButton);
                }

                catch (Exception e)
                {
                    Console.WriteLine("Error while logging in: " + e.Message);
                }
            }
            
            else
            {
                Console.WriteLine("Login to Lingos and press enter...");
                Console.ReadLine();
            }
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