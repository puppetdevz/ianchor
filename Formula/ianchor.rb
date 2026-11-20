class Ianchor < Formula
  desc "Sync config files to iCloud Drive via symlinks"
  homepage "https://github.com/puppetdevz/ianchor"
  version "__IANCHOR_VERSION__"
  license "MIT"

  on_arm do
    url "__IANCHOR_ARM_URL__"
    sha256 "__IANCHOR_ARM_SHA256__"
  end

  on_intel do
    url "__IANCHOR_AMD_URL__"
    sha256 "__IANCHOR_AMD_SHA256__"
  end

  def install
    bin.install "ianchor"
    generate_completions_from_executable(bin/"ianchor", "completion", shells: [:zsh, :bash])
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/ianchor --version")

    assert_match "#compdef ianchor", shell_output("#{bin}/ianchor completion zsh")
    assert_match "complete", shell_output("#{bin}/ianchor completion bash")
  end
end
