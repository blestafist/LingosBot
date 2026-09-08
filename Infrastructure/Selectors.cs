using OpenQA.Selenium;

namespace LingosBotApp;

internal static class Selectors
{
    // Login selectors were verified from https://lingos.pl/h/login.
    // The post-login selectors below intentionally prefer semantic attributes and
    // stable URL fragments over generated React element IDs or utility classes.
    public static SelectorDefinition CookieRejectButton { get; } = SelectorDefinition.CssRequired(
        "CookieRejectButton",
        "#CybotCookiebotDialogBodyLevelButtonLevelOptinDecline, #CybotCookiebotDialogBodyButtonDecline, #CybotCookiebotDialogBodyLevelButtonDecline");

    public static SelectorDefinition LoginEmailInput { get; } = SelectorDefinition.CssRequired(
        "LoginEmailInput",
        "form#login-form input[name='_username']");

    public static SelectorDefinition LoginPasswordInput { get; } = SelectorDefinition.CssRequired(
        "LoginPasswordInput",
        "form#login-form input[name='_password']");

    public static SelectorDefinition LoginSubmitButton { get; } = SelectorDefinition.CssRequired(
        "LoginSubmitButton",
        "#submit-login-button");

    public static SelectorDefinition AuthenticatedShellMarker { get; } = SelectorDefinition.CssOptional(
        "AuthenticatedShellMarker",
        "#app[data-props], a[href='/student/dashboard'], #main-menu-list a[href='/student-confirmed/group']");

    public static SelectorDefinition LeftMenuZestawyButton { get; } = SelectorDefinition.CssRequired(
        "LeftMenuZestawyButton",
        "a[href='/student/wordsets'], #main-menu-list a[href='/student-confirmed/wordsets'], a#menu-small-item-icon-1[href='/student-confirmed/wordsets']");

    public static SelectorDefinition ZestawyPageMarker { get; } = SelectorDefinition.CssOptional(
        "ZestawyPageMarker",
        "TODO: optional selector that confirms the Zestawy page is open");

    public static SelectorDefinition SetCardContainer { get; } = SelectorDefinition.CssRequired(
        "SetCardContainer",
        "ul.mt-6 > li:has(a[href^='/student/wordsets/']), .card.rounded-3.p-3.p-sm-4.nav");

    public static SelectorDefinition SetCardPreviewButton { get; } = SelectorDefinition.CssRequired(
        "SetCardPreviewButton",
        "a[href^='/student/wordsets/'], a.btn[href*='/student-confirmed/wordset/']");

    public static SelectorDefinition SetPreviewContainer { get; } = SelectorDefinition.CssOptional(
        "SetPreviewContainer",
        "TODO: optional selector for a preview container if Lingos switches from page navigation to a modal");

    public static SelectorDefinition PreviewVocabularyRow { get; } = SelectorDefinition.CssRequired(
        "PreviewVocabularyRow",
        "ul.mt-6.flex.flex-col.gap-3 > li:has(div.grid > p:nth-child(2)), .card.rounded-3.p-3.nav.text-dark");

    public static SelectorDefinition PreviewForeignWordCell { get; } = SelectorDefinition.CssRequired(
        "PreviewForeignWordCell",
        "div.grid > p:first-child, .flashcard-border-end");

    public static SelectorDefinition PreviewPolishWordCell { get; } = SelectorDefinition.CssRequired(
        "PreviewPolishWordCell",
        "div.grid > p:nth-child(2), .flashcard-border-start");

    public static SelectorDefinition PreviewCloseButton { get; } = SelectorDefinition.CssOptional(
        "PreviewCloseButton",
        "TODO: optional selector for a preview close/back button when Podgląd opens a modal");

    public static SelectorDefinition MainPageMarker { get; } = SelectorDefinition.CssRequired(
        "MainPageMarker",
        "a[href='/student/dashboard'][aria-current='page'], a[href='/student-confirmed/group'].active, #main-menu-list a[href='/student-confirmed/group']");

    public static SelectorDefinition MainLearnButton { get; } = SelectorDefinition.CssRequired(
        "MainLearnButton",
        "a[href^='/learning/start']");

    // The current dashboard renders this select inside a Headless UI dialog after
    // clicking "Zmień klasę". Older dashboard pages rendered the same select in
    // #modal-change-group, so retain that fallback while avoiding generated IDs.
    public static SelectorDefinition ClassChangeButton { get; } = SelectorDefinition.XPathRequired(
        "ClassChangeButton",
        "//*[self::button or self::a][normalize-space(.)='Zmień klasę']");

    public static SelectorDefinition ClassSelect { get; } = SelectorDefinition.CssRequired(
        "ClassSelect",
        "[role='dialog'] select, #modal-change-group select");

    public static SelectorDefinition ClassSaveButton { get; } = SelectorDefinition.XPathRequired(
        "ClassSaveButton",
        "//div[@role='dialog']//button[@type='submit' and normalize-space(.)='Zapisz']");

    public static SelectorDefinition ClassDialogCloseButton { get; } = SelectorDefinition.XPathRequired(
        "ClassDialogCloseButton",
        "//div[@role='dialog']//button[@aria-label='Zamknij' or @aria-label='Close']");

    public static SelectorDefinition LessonPrompt { get; } = SelectorDefinition.CssRequired(
        "LessonPrompt",
        "#app p.text-2xl strong");

    public static SelectorDefinition LessonAnswerInput { get; } = SelectorDefinition.CssRequired(
        "LessonAnswerInput",
        "#learning-answer");

    public static SelectorDefinition LessonFeedbackMarker { get; } = SelectorDefinition.CssRequired(
        "LessonFeedbackMarker",
        "#app p.text-brand, #app p.text-red-700");

    public static SelectorDefinition LessonContinueButton { get; } = SelectorDefinition.CssRequired(
        "LessonContinueButton",
        // Keep this scoped to the lesson content. The authenticated shell also
        // contains Headless UI popover buttons (and those can remain in the DOM
        // while the lesson redirects to the finished dashboard).
        "#app main button[type='submit'], #app main button[type='button']:not([tabindex='-1']):not([id^='headlessui-popover-'])");

    public static SelectorDefinition LessonProgressCounter { get; } = SelectorDefinition.CssRequired(
        "LessonProgressCounter",
        "#app [role='progressbar']");

    public static SelectorDefinition LessonLimitReachedMarker { get; } = SelectorDefinition.CssRequired(
        "LessonLimitReachedMarker",
        "#main-content a[href*='/learning/start/'].disabled");

    public static SelectorDefinition LessonIncorrectAnswerMarker { get; } = SelectorDefinition.CssRequired(
        "LessonIncorrectAnswerMarker",
        "#app p.text-red-700");

    public static SelectorDefinition LessonFinishedMarker { get; } = SelectorDefinition.CssOptional(
        "LessonFinishedMarker",
        // The completion dialog is rendered in Headless UI's portal, outside
        // #app, so do not scope this to the lesson root.
        "[role='dialog'] h2");

    public static SelectorDefinition ZestawyNextPageButton { get; } = SelectorDefinition.CssOptional(
        "ZestawyNextPageButton",
        "TODO: optional selector for the next-page button on the Zestawy page");

    // --- Wyzwania (challenges) ------------------------------------------
    // The current dashboard embeds challenge data in the app's data-props
    // attribute; the legacy dashboard rendered cards in #wyzwaniaModal.
    public static SelectorDefinition ChallengeCard { get; } = SelectorDefinition.CssRequired(
        "ChallengeCard",
        "#app[data-props], #wyzwaniaModal .bg-secondary");

}

internal enum SelectorStrategy
{
    Css,
    XPath
}

internal sealed class SelectorDefinition
{
    private SelectorDefinition(string name, SelectorStrategy strategy, string value, bool optional)
    {
        Name = name;
        Strategy = strategy;
        Value = value;
        Optional = optional;
    }

    public string Name { get; }

    public SelectorStrategy Strategy { get; }

    public string Value { get; }

    public bool Optional { get; }

    public bool IsPlaceholder => Value.StartsWith("TODO", StringComparison.OrdinalIgnoreCase);

    public bool IsConfigured => !string.IsNullOrWhiteSpace(Value) && !IsPlaceholder;

    public static SelectorDefinition CssRequired(string name, string value) => new(name, SelectorStrategy.Css, value, optional: false);

    public static SelectorDefinition CssOptional(string name, string value) => new(name, SelectorStrategy.Css, value, optional: true);

    public static SelectorDefinition XPathRequired(string name, string value) => new(name, SelectorStrategy.XPath, value, optional: false);

    public static SelectorDefinition XPathOptional(string name, string value) => new(name, SelectorStrategy.XPath, value, optional: true);

    public By ToBy()
    {
        if (!IsConfigured)
        {
            var mode = Optional ? "optional" : "required";
            throw new InvalidOperationException(
                $"The {mode} selector '{Name}' in Selectors.cs still contains a TODO placeholder. Replace it before the bot can use that part of the site.");
        }

        return Strategy switch
        {
            SelectorStrategy.Css => By.CssSelector(Value),
            SelectorStrategy.XPath => By.XPath(Value),
            _ => throw new InvalidOperationException($"Unsupported selector strategy '{Strategy}'.")
        };
    }

    public bool TryToBy(out By? by)
    {
        if (!IsConfigured)
        {
            by = null;
            return false;
        }

        by = Strategy switch
        {
            SelectorStrategy.Css => By.CssSelector(Value),
            SelectorStrategy.XPath => By.XPath(Value),
            _ => throw new InvalidOperationException($"Unsupported selector strategy '{Strategy}'.")
        };

        return true;
    }
}
