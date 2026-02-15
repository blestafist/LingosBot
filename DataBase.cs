using Newtonsoft.Json;
using OpenQA.Selenium.Support.Events;

namespace LingosBot
{

    public class Config
    {
        public bool automaticLogin = true;
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

    public class WordsDataBaseTweaks 
    {
        private WordsDataBase dataBase;
        public void InitDB()
        {
            if (!File.Exists(Bot.config.wordsDataBasePath))
            {
                Console.WriteLine("Could not find database path, creating a new file");

                WordsDataBase empty = new();
                File.WriteAllText(Bot.config.wordsDataBasePath, JsonConvert.SerializeObject(empty));
            }

            dataBase = JsonConvert.DeserializeObject<WordsDataBase>(File.ReadAllText(Bot.config.wordsDataBasePath));
        }
        public bool ExistsInDatabase(string word)
        {
            return dataBase.words.ContainsKey(word);
        }

        public string ReturnTranslation(string key)
        {
            return dataBase.words[key];
        }

        public void WriteToDB(string key, string value)
        {
            dataBase.words.Add(key, value);
            File.WriteAllText(Bot.config.wordsDataBasePath, JsonConvert.SerializeObject(dataBase, Formatting.Indented));
        }
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