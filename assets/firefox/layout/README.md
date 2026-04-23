# Initra Firefox Layout

This folder contains a non-sensitive Firefox UI layout bundle captured with:

```powershell
dist\initra.exe --capture-firefox-layout
```

Included:

- toolbar and button placement via `browser.uiCustomization.state`
- bookmark toolbar visibility
- selected safe UI toggles such as `sidebar.revamp`

Intentionally excluded:

- passwords
- cookies
- browsing history
- bookmarks
- `logins.json`
- `key4.db`
- `places.sqlite`
- `xulstore.json` window positions and screen coordinates
