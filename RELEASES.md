# Publishing a Release

1. create a new tag (e.g. v1.2.3)

```
git tag v1.2.3
git push origin v1.2.3
```

2. do a release build

```
VERSION=v1.2.3 ./release.bash
```

this creates the following tarballs in the `release` directory:

- ankh-darwin-amd64.tar.gz
- ankh-linux-amd64.tar.gz

3. author a new release on [github](https://github.com/appnexus/ankh/releases/new)

   1. write a description of what was changed
   2. upload tarballs to the newly authored github release

4. update homebrew [formula](Formula/README.md)
