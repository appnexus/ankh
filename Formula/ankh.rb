class Ankh < Formula
  desc "Another Kubernetes Helper"
  homepage "https://github.com/appnexus/ankh"
  url "https://github.com/appnexus/ankh/archive/v2.1.1.tar.gz"
  sha256 "6510feda09767d3df4443f610cec2fa105360d2a2edb2a3bf7d58cc73b345649"

  depends_on "go" => :build
  depends_on "kubernetes-helm"

  def install
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
