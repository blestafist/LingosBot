namespace LingosBotApp;

internal sealed class CredentialStore(AppConfig config)
{
    private readonly AppConfig _config = config;

    public bool TryLoad(out AppCredentials? credentials)
    {
        credentials = _config.Credentials;

        if (credentials is null || string.IsNullOrWhiteSpace(credentials.Email) || string.IsNullOrWhiteSpace(credentials.Password))
        {
            credentials = null;
            return false;
        }

        credentials = credentials with { Email = credentials.Email.Trim() };
        return true;
    }

    public void Save(AppCredentials credentials)
    {
        _config.Credentials = credentials with { Email = credentials.Email.Trim() };
        _config.Save();
    }

    public void Delete()
    {
        _config.Credentials = null;
        _config.Save();
    }
}

internal sealed record AppCredentials(string Email, string Password);
