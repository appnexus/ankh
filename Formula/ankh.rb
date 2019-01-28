class Ankh < Formula
  desc "Another Kubernetes Helper"
  homepage "https://github.com/appnexus/ankh"
  version "2.0.0-beta.4"
  url "https://github.com/appnexus/ankh/releases/download/${version}/ankh-darwin-amd64.tar.gz"
  sha256 "7e9879a0c69ad9ae4135e9f3ab22221dd70bcec0f5e6feafaab2d4dd4a227701"

  def install
    bin.install "ankh"
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
