using System.ComponentModel.DataAnnotations.Schema;
using OpenQA.Selenium;
using OpenQA.Selenium.Support.UI;
using SeleniumExtras.WaitHelpers;


namespace LingosBot
{

    internal class Bot
    {
        public static Config config = ConfigDataBaseTweaks.GetConfig(); // getting user config
        public static WordsDataBaseTweaks dataBase = new();
        public static IWebDriver webDriver = Helpers.GetWebDriver();


        public static void Main(string[] args)  // program entry point
        {
            if (OperatingSystem.IsWindows()) { Console.WriteLine("INSTALL LINUX!"); }
            else if (OperatingSystem.IsLinux()) { Console.WriteLine("LINUX USER :)))"); }


            webDriver.Navigate().GoToUrl("https://lingos.pl/h/login"); // go to lingos url
            SeleniumMethods.Login();  // logging in
            dataBase.InitDB(); // Initialising words DB

            for (int i = 0; i < config.numberOfLessons; i++)
            {
                SeleniumMethods.LaunchLesson();

                SeleniumMethods.DoLesson();
            }

            webDriver.Quit();
        }
    }



    internal static class SeleniumMethods // a static class for selenium methods, such as Login(), DoLesson() and others
    {
        
        public static void Login() // login to Lingos
        {
            if (Bot.config.automaticLogin)
            {
                try
                {
                    string login = Bot.config.email; // reading config login
                    string password = Bot.config.password;  // and password

                    var declineCookies = Helpers.WaitForElement(By.Id("CybotCookiebotDialogBodyButtonDecline"), 15, ExpectedConditions.ElementToBeClickable(By.Id("CybotCookiebotDialogBodyButtonDecline")));
                    declineCookies.Click(); // decline fucking cookies

                    var loginBox = Helpers.WaitForElement(By.Name("login"), 10, ExpectedConditions.ElementToBeClickable(By.Name("login")));
                    var passwordBox = Bot.webDriver.FindElement(By.Name("password"));
                    var submitButton = Bot.webDriver.FindElement(By.Id("submit-login-button")); // find elemets of login

                    loginBox.SendKeys(login); // enter login
                    passwordBox.SendKeys(password); // enter password
                    ((IJavaScriptExecutor)Bot.webDriver).ExecuteScript("arguments[0].click();", submitButton); // click button via JS (element to be clickable)
                }

                catch (Exception e) // exc
                {
                    Console.WriteLine("Error while logging in: " + e.Message);
                }
            }

            else // if user wants no auto login
            {
                Console.WriteLine("Login to Lingos and press enter...");
                Console.ReadLine();
            }
        }

        public static void LaunchLesson() // launch lesson
        {
            try
            {
                var launchLessonButton = Helpers.WaitForElement(By.PartialLinkText("UCZ SIĘ"));  // find element by part. name (button UCZ SIE)
                ((IJavaScriptExecutor)Bot.webDriver).ExecuteScript("arguments[0].click()", launchLessonButton); // clicking with JavaScript

                Helpers.WaitForElement(By.Id("flashcard_main_text"));
            }

            catch (Exception e) // handling exc
            {
                Console.WriteLine("Error while starting lesson. Message: " + e.Message);
            }
        }
        
        public static void DoLesson()
        {
            while (true)
            {
                if (Bot.webDriver.PageSource.Contains("Lekcja wykonana")) { return; } // if page contains UCZ SIE, end lesson
                else if (Bot.webDriver.PageSource.Contains("Przetłumacz")) // not completed yet
                {
                    Console.WriteLine("2nd");
                    var wordToTranslate = Helpers.WaitForElement(By.Id("flashcard_main_text")).Text;
                    var inputField = Helpers.WaitForElement(By.Id("flashcard_answer_input"), 15, ExpectedConditions.ElementToBeClickable(By.Id("flashcard_answer_input")));

                    if (Bot.dataBase.ExistsInDatabase(wordToTranslate))
                    {
                        inputField.SendKeys(Bot.dataBase.ReturnTranslation(wordToTranslate));
                        Helpers.ClickEnter();

                        Helpers.WaitForElement(By.Id("flashcard_error_correct"));
                        Helpers.ClickEnter();
                    }

                    else
                    {
                        Helpers.ClickEnter();
                        var correctWord = Helpers.WaitForElement(By.Id("flashcard_error_correct"), 10).Text;
                        Console.WriteLine("I see the correct word, it is " + correctWord);
                        Bot.dataBase.WriteToDB(wordToTranslate, correctWord);
                        Helpers.ClickEnter();
                    }
                }

                else if (Bot.webDriver.PageSource.Contains("Nowe słowo"))
                {
                    // write new word in DB
                }
            }
        }
    }

    
}
