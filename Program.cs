namespace LingosBotApp;

internal static class Program
{
    private static int Main()
    {
        Console.WriteLine("LingosBot");

        try
        {
            var config = AppConfig.Load();
            var bot = new LingosBot(config, new BrowserFactory());
            bot.Run(config.LessonCount);
            return 0;
        }
        catch (Exception ex)
        {
            Console.Error.WriteLine($"LingosBot failed: {ex.Message}");
            return 1;
        }
    }
}
