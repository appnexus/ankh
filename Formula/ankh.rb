class Ankh < Formula
  desc "Another Kubernetes Helper"
  homepage "https://github.com/appnexus/ankh"
  url "https://github.com/appnexus/ankh/archive/v2.0.0-beta.5.tar.gz"
  sha256 "a5fcc3756d773f03810b3b361c5d86b97f8333eede75adb371bd757ec79fe127"

  depends_on "go" => :build
  depends_on "kubernetes-helm"

  def install
    ENV["GOPATH"] = buildpath
    (buildpath/"src/github.com/appnexus/ankh").install buildpath.children
    cd "src/github.com/appnexus/ankh/ankh" do
      system "go", "build", "-ldflags", "-X main.AnkhBuildVersion=#{version}"
      bin.install "ankh"
    end
  end

  test do
    (testpath/"ankhconfig.yaml").write <<~EOS
      include:
        - minikube.yaml
      environments:
        env-minikube:
      contexts:
        ctx-minikube:
    EOS
    assert_match /^ctx-minikube/, pipe_output("#{bin}/ankh --ankhconfig ankhconfig.yaml config get-contexts")
    assert_match /^env-minikube/, pipe_output("#{bin}/ankh --ankhconfig ankhconfig.yaml config get-environments")
  end
end
