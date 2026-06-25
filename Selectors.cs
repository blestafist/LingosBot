using OpenQA.Selenium;

namespace LingosBot;

internal static class Selectors
{
    // Login page
    public static SelectorDefinition CookieDeclineButton { get; } = SelectorDefinition.IdRequired(
        "CookieDeclineButton",
        "CybotCookiebotDialogBodyButtonDecline");

    public static SelectorDefinition LoginEmailInput { get; } = SelectorDefinition.NameRequired(
        "LoginEmailInput",
        "login");

    public static SelectorDefinition LoginPasswordInput { get; } = SelectorDefinition.NameRequired(
        "LoginPasswordInput",
        "password");

    public static SelectorDefinition LoginSubmitButton { get; } = SelectorDefinition.IdRequired(
        "LoginSubmitButton",
        "submit-login-button");

    // Main page
    public static SelectorDefinition MainLearnButton { get; } = SelectorDefinition.PartialLinkTextRequired(
        "MainLearnButton",
        "UCZ SIĘ");

    // Lesson page
    public static SelectorDefinition LessonPrompt { get; } = SelectorDefinition.IdRequired(
        "LessonPrompt",
        "flashcard_main_text");

    public static SelectorDefinition LessonAnswerInput { get; } = SelectorDefinition.IdRequired(
        "LessonAnswerInput",
        "flashcard_answer_input");

    public static SelectorDefinition LessonCorrectAnswer { get; } = SelectorDefinition.IdRequired(
        "LessonCorrectAnswer",
        "flashcard_error_correct");

    public static SelectorDefinition LessonContinueButton { get; } = SelectorDefinition.IdRequired(
        "LessonContinueButton",
        "enterBtn");
}

internal enum SelectorStrategy
{
    Id,
    Name,
    PartialLinkText,
    Css,
    XPath
}

internal sealed class SelectorDefinition
{
    private SelectorDefinition(string name, SelectorStrategy strategy, string value)
    {
        Name = name;
        Strategy = strategy;
        Value = value;
    }

    public string Name { get; }
    public SelectorStrategy Strategy { get; }
    public string Value { get; }

    public static SelectorDefinition IdRequired(string name, string value) =>
        new(name, SelectorStrategy.Id, value);

    public static SelectorDefinition NameRequired(string name, string value) =>
        new(name, SelectorStrategy.Name, value);

    public static SelectorDefinition PartialLinkTextRequired(string name, string value) =>
        new(name, SelectorStrategy.PartialLinkText, value);

    public static SelectorDefinition CssRequired(string name, string value) =>
        new(name, SelectorStrategy.Css, value);

    public static SelectorDefinition XPathRequired(string name, string value) =>
        new(name, SelectorStrategy.XPath, value);

    public By ToBy()
    {
        return Strategy switch
        {
            SelectorStrategy.Id => By.Id(Value),
            SelectorStrategy.Name => By.Name(Value),
            SelectorStrategy.PartialLinkText => By.PartialLinkText(Value),
            SelectorStrategy.Css => By.CssSelector(Value),
            SelectorStrategy.XPath => By.XPath(Value),
            _ => throw new InvalidOperationException($"Unsupported selector strategy '{Strategy}'.")
        };
    }
}
