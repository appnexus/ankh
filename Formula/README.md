# Maintaining Homebrew Formula

In order to update to a new release:

1. go to https://github.com/appnexus/ankh/releases to get the URL for the **source** tarball

2. `brew create <release tarball url>` - note the line of the output "For your reference the SHA-256 is: <sha-256>"

3. update Formula/ankh.rb with:

```
url "`<release tarball url>`"
sha256 "`<sha-256>`"
```

If you get an error like:

```
Error: /usr/local/Homebrew/Library/Taps/homebrew/homebrew-core/Formula/ankh.rb already exists
```

Simply move it out of the way:

```
$ mv /usr/local/Homebrew/Library/Taps/homebrew/homebrew-core/Formula/ankh.rb{,.bak}
```
