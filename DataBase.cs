using System.Net.Quic;
using Newtonsoft.Json;
using OpenQA.Selenium.DevTools.V138.Network;

namespace LingosBot
{

    public class Config
    {
        public string email = "ENTER YOUR LINGOS EMAIL HERE";
        public string password = "ENTER YOUR LINGOS PASSWORD HERE";
        public int numberOfLessons = 1; // number of lessons to do
        public string browser = "Chrome";

        public string wordsDataBasePath = "words.json";
    }

    public class WordsDataBase
    {
        public Dictionary<string, string> words = []; // key is an original word, value is a translated word
    }
    
    public static class ConfigDataBaseTweaks
    {
        private const string filePath = "config.json";
        public static Config GetConfig()
        {

            if (!File.Exists(filePath)) {
                File.WriteAllText(filePath, JsonConvert.SerializeObject(new Config(), Formatting.Indented));

                Console.ForegroundColor = ConsoleColor.Red;
                Console.WriteLine("Enter appropriate values for you in config.json file and relaunch the program! ");
                
                Environment.Exit(0); // quit the program
            }
            
            Config config = JsonConvert.DeserializeObject<Config>(File.ReadAllText(filePath));
            return config ?? new Config();
        }
    }
}