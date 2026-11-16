class Ianchor < Formula
  desc "Sync config files to iCloud Drive via symlinks"
  homepage "https://github.com/puppetdevz/ianchor"
  url "__IANCHOR_URL__"
  version "__IANCHOR_VERSION__"
  sha256 "__IANCHOR_SHA256__"
  license "MIT"

  depends_on "go" => :build

  def install
    ENV["GOTOOLCHAIN"] = "local"
    ENV["CGO_ENABLED"] = "0"
    system "go", "build", "-trimpath", "-buildvcs=false", "-o", bin/"ianchor", "./cmd/ianchor"
    generate_completions_from_executable(bin/"ianchor", "completion", shells: [:zsh, :bash])
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/ianchor --version")

    home = testpath/"home"
    cloud = home/"Library/Mobile Documents/com~apple~CloudDocs"
    cloud.mkpath
    (home/".zshrc").write "export IANCHOR_TEST=1\n"
    ENV["HOME"] = home.to_s

    system bin/"ianchor", "init", "--dir", "ianchor"
    system bin/"ianchor", "add", home/".zshrc"
    assert_predicate home/".zshrc", :symlink?
    assert_match "linked", shell_output("#{bin}/ianchor list")

    system bin/"ianchor", "recover", "zshrc"
    assert_equal "export IANCHOR_TEST=1\n", (home/".zshrc").read
    refute_predicate home/".zshrc", :symlink?
  end
end
