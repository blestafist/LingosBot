using OpenQA.Selenium;
using OpenQA.Selenium.Support.UI;
using SeleniumExtras.WaitHelpers;


namespace LingosBot
{

    internal class Bot
    {
        public static Config config = ConfigDataBaseTweaks.GetConfig(); // getting user config
        public static AppConfig appConfig = new AppConfig(config);
        public static WordsDataBaseTweaks dataBase = new();
        public static IWebDriver webDriver = new BrowserFactory().Create(config);
        public static Random rnd = new(); // random generator for making errors with chance


        public static void Main(string[] args)  // program entry point
        {
            if (OperatingSystem.IsWindows()) { Console.WriteLine("INSTALL LINUX!"); }
            else if (OperatingSystem.IsLinux()) { Console.WriteLine("LINUX USER :)))"); }


            webDriver.Navigate().GoToUrl(appConfig.LoginUrl);
            var loginService = new LoginService(webDriver, appConfig);
            loginService.Login();
            dataBase.InitDB();

            for (int i = 0; i < appConfig.NumberOfLessons; i++)
            {
                SeleniumMethods.LaunchLesson();

                SeleniumMethods.DoLesson();
            }

            webDriver.Quit();
        }
    }



    internal static class SeleniumMethods
    {
        public static void LaunchLesson()
        {
            try
            {
                // If still on lesson page, navigate back to main page
                var currentUrl = Bot.webDriver.Url ?? string.Empty;
                if (currentUrl.Contains("/s/lesson/", StringComparison.OrdinalIgnoreCase))
                {
                    Console.WriteLine("Returning to main page...");
                    Bot.webDriver.Navigate().GoToUrl(Bot.appConfig.StudentDashboardUrl);
                }

                var launchLessonButton = Helpers.WaitForElement(Selectors.MainLearnButton.ToBy());
                ((IJavaScriptExecutor)Bot.webDriver).ExecuteScript("arguments[0].click()", launchLessonButton);

                Helpers.WaitForElement(Selectors.LessonPrompt.ToBy());
            }

            catch (Exception e)
            {
                Console.WriteLine("Error while starting lesson. Message: " + e.Message);
            }
        }
        
        public static void DoLesson()
        {
            while (true)
            {
                if (Bot.webDriver.PageSource.Contains("UCZ")) { return; }
                else if (Bot.webDriver.PageSource.Contains("Przetłumacz"))
                {
                    Console.WriteLine("2nd");
                    var wordToTranslate = Helpers.WaitForElement(Selectors.LessonPrompt.ToBy()).Text;
                    var inputField = Helpers.WaitForElement(Selectors.LessonAnswerInput.ToBy(), 15,
                        ExpectedConditions.ElementToBeClickable(Selectors.LessonAnswerInput.ToBy()));

                    if (Bot.dataBase.ExistsInDatabase(wordToTranslate))
                    {
                        bool needAnError = Helpers.MakeAnError();
                        if (!needAnError)
                        {
                            var translation = Bot.dataBase.ReturnTranslation(wordToTranslate);
                            if (!Helpers.TryFastFillInput(inputField, translation))
                            {
                                inputField.SendKeys(translation);
                            }
                        }

                        Helpers.ClickEnter();
                        Helpers.WaitForElement(Selectors.LessonCorrectAnswer.ToBy());

                        var enterBtn = Helpers.WaitForElement(Selectors.LessonContinueButton.ToBy(), 10,
                            ExpectedConditions.ElementToBeClickable(Selectors.LessonContinueButton.ToBy()));
                        bool isWrong = enterBtn.GetAttribute("class")?.Contains("btn-danger") == true;

                        if (isWrong)
                        {
                            var correctWord = Bot.webDriver.FindElement(Selectors.LessonCorrectAnswer.ToBy()).Text;
                            Console.WriteLine($"Wrong answer! Correct word is: {correctWord}");
                        }
                        else
                        {
                            Console.WriteLine("Correct answer!");
                        }

                        Helpers.ClickEnter();

                        new WebDriverWait(Bot.webDriver, TimeSpan.FromSeconds(10))
                            .Until(d => d.FindElements(Selectors.LessonCorrectAnswer.ToBy()).Count == 0);

                        if (Bot.webDriver.PageSource.Contains("UCZ")) { return; }

                        try
                        {
                            inputField = Helpers.WaitForElement(Selectors.LessonAnswerInput.ToBy());
                        }
                        catch
                        {
                            return;
                        }
                    }
                    else
                    {
                        Helpers.ClickEnter();
                        var correctWord = Helpers.WaitForElement(Selectors.LessonCorrectAnswer.ToBy(), 10).Text;
                        Console.WriteLine("I see the correct word, it is " + correctWord);
                        Bot.dataBase.WriteToDB(wordToTranslate, correctWord);
                        Helpers.ClickEnter();
                    }
                }
                else if (Bot.webDriver.PageSource.Contains("Nowe słowo"))
                {
                    Helpers.ClickEnter();
                }
            }
        }
    }

    
}
