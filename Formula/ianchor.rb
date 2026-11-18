class Ianchor < Formula
  desc "Sync config files to iCloud Drive via symlinks"
  homepage "https://github.com/puppetdevz/ianchor"
  url "__IANCHOR_URL__"
  version "__IANCHOR_VERSION__"
  sha256 "__IANCHOR_SHA256__"
  license "MIT"

  depends_on "go" => :build

  deny_network_access!

  def fetch
    ENV["GOTOOLCHAIN"] = "local"
    system "go", "mod", "download"
    # Older Homebrew versions do not run the fetch phase.
    (buildpath/".ianchor-modules-fetched").write ""
  end

  def install
    unless (buildpath/".ianchor-modules-fetched").exist?
      odie "iAnchor requires Homebrew 6.0.20 or newer. Run brew update and retry."
    end
    ENV["GOTOOLCHAIN"] = "local"
    ENV["CGO_ENABLED"] = "0"
    # Modules and their checksums were fetched and verified before this phase.
    ENV["GOPROXY"] = "off"
    ENV["GONOPROXY"] = "none"
    ENV["GOSUMDB"] = "off"
    system "go", "build", *std_go_args, "-buildvcs=false", "./cmd/ianchor"
    generate_completions_from_executable(bin/"ianchor", "completion", shells: [:zsh, :bash])
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/ianchor --version")

    assert_match "#compdef ianchor", shell_output("#{bin}/ianchor completion zsh")
    assert_match "complete", shell_output("#{bin}/ianchor completion bash")
  end
end
