# Maintaining Homebrew Formula

## Update Formula

This should be done each time a release is cut. See: [RELEASES](../RELEASES.md)

1. go to https://github.com/appnexus/ankh/releases to get the URL for the **source** tarball

the URL here will actually redirect to a different domain.

*example*

```sh
$ curl https://github.com/appnexus/ankh/archive/v2.1.0.tar.gz

<html><body>You are being <a href="https://codeload.github.com/appnexus/ankh/tar.gz/v2.1.0">redirected</a>.</body></html>
```

2. get sha256

```sh
$ curl -s <source tarball url> | shasum -a 256 -
<sha-256 result> -
```

*example:*

```sh
$ curl -s https://codeload.github.com/appnexus/ankh/tar.gz/v2.1.0 | shasum -a 256 -
770e8e5bacb91b93985ea05f2fcd3ea30faf8ad0a4fda32b61164cd051c29042  -
```

3. update Formula/ankh.rb with:

```
url "`<source tarball url>`"
sha256 "`<sha-256 result>`"
```

*example:*

```
url "https://github.com/appnexus/ankh/archive/v2.1.0.tar.gz"
sha256 "770e8e5bacb91b93985ea05f2fcd3ea30faf8ad0a4fda32b61164cd051c29042"
```

## Troubleshooting

If you get an error like:

```
Error: /usr/local/Homebrew/Library/Taps/homebrew/homebrew-core/Formula/ankh.rb already exists
```

Simply move it out of the way:

```
$ mv /usr/local/Homebrew/Library/Taps/homebrew/homebrew-core/Formula/ankh.rb{,.bak}
```
