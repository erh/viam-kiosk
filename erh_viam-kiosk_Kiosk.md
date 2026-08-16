# Model erh:viam-kiosk:Kiosk

Provide a description of the model and any relevant information.

## Configuration

```json
{
   "url" : "<url>",
   "refresh_seconds" : 0,
   "resolution" : "1920x1080",
   "scale" : 2
}
```

- `url` (required): the page to display.
- `refresh_seconds` (optional): restart the browser every N seconds.
- `resolution` (optional): set the display output mode, e.g. `"1280x720"`
  or `"1920x1080@60"`. Requires `wlr-randr` (installed by first_run) and
  cage >= 0.1.5. Omit for the display's native mode.
- `scale` (optional): enlarge everything by this factor (e.g. `2` on a
  high-dpi/4K display where the page renders too small). Fractional
  values like `1.5` work too. Omit or `0` for the default.
