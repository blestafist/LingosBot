using System.Diagnostics;
using OpenQA.Selenium;
using OpenQA.Selenium.Support.Extensions;

namespace LingosBotApp;

internal sealed class LingosBot(
    AppConfig config,
    BrowserFactory browserFactory)
{
    private readonly AppConfig _config = config;
    private readonly BrowserFactory _browserFactory = browserFactory;

    public void Run(int lessonCount)
    {
        var credentials = ResolveCredentials();
        var completedLessons = 0;

        IWebDriver? driver = null;

        try
        {
            Console.WriteLine($"Starting {_config.Browser}...");
            driver = _browserFactory.Create(_config);
            var totalStopwatch = Stopwatch.StartNew();

            var loginService = new LoginService(driver, _config);
            LoginWithRetry(loginService, credentials);

            var classRunner = new ClassRunner(driver, _config);
            var classes = classRunner.ReadClasses();
            Console.WriteLine($"Found {classes.Count} class(es) to process.");
            var challengeRunner = new ChallengeRunner(driver, _config);

            for (var classIndex = 0; classIndex < classes.Count; classIndex++)
            {
                var @class = classes[classIndex];
                var classLessonCount = ResolveLessonCount(@class);
                Console.WriteLine();
                Console.WriteLine($"=== Class {classIndex + 1}/{classes.Count}: {@class.Title} ({classLessonCount} lesson(s)) ===");

                if (classLessonCount == 0)
                {
                    Console.WriteLine("Skipped by classLessonCounts.");
                    continue;
                }

                classRunner.Select(@class);
                completedLessons += RunLessonsForClass(driver, challengeRunner, classLessonCount, @class.Title);
            }

            totalStopwatch.Stop();
            Console.WriteLine($"Total run time: {totalStopwatch.Elapsed:mm\\:ss}.");
            Console.WriteLine($"Completed {completedLessons} lesson(s) across {classes.Count} class(es).");
        }
        catch (Exception ex) when (driver is not null)
        {
            SaveDiagnostics(driver, ex);
            throw;
        }
        finally
        {
            if (driver is not null)
            {
                CloseDriver(driver);
            }
        }
    }

    public IReadOnlyList<ClassInfo> ScanClasses()
    {
        var credentials = ResolveCredentials();
        IWebDriver? driver = null;

        try
        {
            Console.WriteLine($"Starting {_config.Browser} to scan classes...");
            driver = _browserFactory.Create(_config);
            var loginService = new LoginService(driver, _config);
            LoginWithRetry(loginService, credentials);

            var classes = new ClassRunner(driver, _config).ReadClasses();
            Console.WriteLine($"Found {classes.Count} class(es):");

            foreach (var @class in classes)
            {
                Console.WriteLine($"- {@class.Title}");
            }

            return classes;
        }
        catch (Exception ex) when (driver is not null)
        {
            SaveDiagnostics(driver, ex);
            throw;
        }
        finally
        {
            if (driver is not null)
            {
                CloseDriver(driver);
            }
        }
    }

    private void CloseDriver(IWebDriver driver)
    {
        Console.WriteLine($"Closing {_config.Browser}...");

        try
        {
            driver.Quit();
        }
        catch (Exception ex)
        {
            Console.WriteLine($"Browser cleanup reported an error: {ex.Message}");
        }
        finally
        {
            try
            {
                driver.Dispose();
            }
            catch (Exception ex)
            {
                Console.WriteLine($"Browser disposal reported an error: {ex.Message}");
            }
        }
    }

    private int RunLessonsForClass(IWebDriver driver, ChallengeRunner challengeRunner, int lessonCount, string classTitle)
    {
        var vocabularyCollector = new VocabularyCollector(driver, _config);
        var vocabularyStopwatch = Stopwatch.StartNew();
        var vocabulary = vocabularyCollector.CollectVocabulary();
        vocabularyStopwatch.Stop();

        if (vocabulary.Count == 0)
        {
            throw new InvalidOperationException("No vocabulary was collected from Zestawy. Verify the selectors in Selectors.cs.");
        }

        var totalStoredAnswers = vocabulary.Sum(pair => pair.Value.Count);
        Console.WriteLine(
            $"Collected {vocabulary.Count} unique Polish prompts with {totalStoredAnswers} stored answer candidates in {vocabularyStopwatch.Elapsed:mm\\:ss}.");

        var lessonRunner = new LessonRunner(driver, _config, vocabulary);
        var completedLessons = 0;

        for (var lessonNumber = 1; lessonNumber <= lessonCount; lessonNumber++)
        {
            // Always make sure we're working toward a Wyzwania challenge first.
            var perfectionismActive = EnsureChallengeSelected(challengeRunner);

            var lessonStopwatch = Stopwatch.StartNew();
            try
            {
                lessonRunner.RunLesson(lessonNumber, perfectionismActive);
            }
            catch (LessonLimitReachedException ex)
            {
                Console.WriteLine($"{ex.Message} Skipping the remaining lessons for '{classTitle}'.");
                break;
            }

            lessonStopwatch.Stop();
            Console.WriteLine($"Lesson {lessonNumber} elapsed time: {lessonStopwatch.Elapsed:mm\\:ss}.");
            completedLessons++;
        }

        return completedLessons;
    }

    private int ResolveLessonCount(ClassInfo @class)
    {
        var overrideValue = _config.ClassLessonCounts.FirstOrDefault(pair =>
            string.Equals(pair.Key, @class.Title, StringComparison.OrdinalIgnoreCase));

        return string.IsNullOrEmpty(overrideValue.Key) ? _config.LessonCount : overrideValue.Value;
    }

    // Before each lesson, make sure a Wyzwania challenge is selected: if one is
    // already active it just continues it; otherwise it joins the highest-point
    // one available. Never throws - a challenge hiccup must not block the lessons.
    private bool EnsureChallengeSelected(ChallengeRunner challengeRunner)
    {
        if (!Selectors.ChallengeCard.IsConfigured)
        {
            return false;
        }

        try
        {
            var snapshot = challengeRunner.ReadChallenges();

            var perfectionism = snapshot.ActivePerfectionism;
            if (perfectionism is not null)
            {
                Console.WriteLine($"Challenge in progress: '{perfectionism.Title}' - intentional errors are disabled for this lesson.");
                return true;
            }

            var active = snapshot.Active;
            if (active is not null)
            {
                Console.WriteLine($"Challenge in progress: '{active.Title}' - this lesson counts toward it.");
                return false;
            }

            var best = snapshot.BestAvailable;
            if (best is not null)
            {
                Console.WriteLine($"Picking challenge '{best.Title}' (worth {best.Points} pkt)...");
                challengeRunner.Join(best);

                // Join refreshes the dashboard, but confirm the server now
                // reports the challenge as active before changing lesson behavior.
                return challengeRunner.ReadChallenges().ActivePerfectionism is not null;
            }
            else
            {
                Console.WriteLine("No challenge available to pick right now - running a normal lesson.");
            }

            return false;
        }
        catch (Exception ex)
        {
            Console.WriteLine($"Could not check challenges ({ex.Message}). Continuing with the lesson anyway.");
            return false;
        }
    }

    private void SaveDiagnostics(IWebDriver driver, Exception exception)
    {
        try
        {
            var diagnosticsDirectory = Path.Combine(AppContext.BaseDirectory, "diagnostics");
            Directory.CreateDirectory(diagnosticsDirectory);

            var timestamp = DateTime.Now.ToString("yyyyMMdd-HHmmss");
            var htmlPath = Path.Combine(diagnosticsDirectory, $"page-{timestamp}.html");
            var screenshotPath = Path.Combine(diagnosticsDirectory, $"page-{timestamp}.png");
            var errorPath = Path.Combine(diagnosticsDirectory, $"error-{timestamp}.txt");

            File.WriteAllText(htmlPath, driver.PageSource);

            if (driver is ITakesScreenshot screenshotDriver)
            {
                var screenshot = screenshotDriver.GetScreenshot();
                screenshot.SaveAsFile(screenshotPath);
            }

            File.WriteAllText(
                errorPath,
                $"Timestamp: {DateTime.Now:O}{Environment.NewLine}Url: {driver.Url}{Environment.NewLine}Error: {exception}");

            Console.WriteLine($"Diagnostics saved to: {diagnosticsDirectory}");
        }
        catch (Exception diagnosticsEx)
        {
            Console.WriteLine($"Could not save diagnostics: {diagnosticsEx.Message}");
        }
    }

    private AppCredentials ResolveCredentials()
    {
        return _config.Credentials! with { Email = _config.Credentials.Email.Trim() };
    }

    private static void LoginWithRetry(LoginService loginService, AppCredentials credentials)
    {
        try
        {
            loginService.Login(credentials);
        }
        catch (LoginFailedException ex)
        {
            throw new InvalidOperationException(
                "Login failed. Check credentials.email and credentials.password in config.json.", ex);
        }
    }
}
