// Checks the licences of the Java client's dependency closure against the ADR 0006 allowlist
// (MIT, BSD, Apache-2.0, ISC). Run by `task licenses`:
//
//   mvn -q dependency:list -DincludeScope=runtime -DoutputFile=target/dependencies-runtime.txt
//   mvn -q dependency:list -DincludeScope=provided -DoutputFile=target/dependencies-provided.txt
//   java -Drepo=<local repository> scripts/CheckLicenses.java target/dependencies-*.txt
//
// The runtime closure is empty by design (javax.crypto, java.net.http and an internal JSON
// parser), so a compile- or runtime-scoped dependency fails the check whatever its licence.
// JSpecify (provided scope: compile-only annotations, `requires static`) is checked against the
// allowlist, because consumers may put it on their compile path for Kotlin nullability. There are
// no exceptions.

import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

public final class CheckLicenses {
  private static final Pattern ARTIFACT =
      Pattern.compile("^\\s*([\\w.-]+):([\\w.-]+):(?:[\\w.-]+:)?([\\w.-]+):([\\w.-]+)(?:\\s.*)?$");
  private static final Pattern LICENSE_NAME =
      Pattern.compile("<license>\\s*(?:<!--.*?-->\\s*)*<name>([^<]+)</name>", Pattern.DOTALL);
  private static final Pattern PARENT =
      Pattern.compile(
          "<parent>.*?<groupId>([^<]+)</groupId>.*?<artifactId>([^<]+)</artifactId>.*?<version>([^<]+)</version>.*?</parent>",
          Pattern.DOTALL);

  private CheckLicenses() {}

  static boolean allowed(String name) {
    String n = name.toLowerCase(Locale.ROOT);
    return n.contains("apache") && n.contains("2")
        || n.equals("mit")
        || n.contains("mit license")
        || n.startsWith("bsd")
        || n.contains("bsd license")
        || n.contains("bsd-")
        || n.equals("isc")
        || n.contains("isc license");
  }

  static List<String> licenses(Path repo, String group, String artifact, String version)
      throws Exception {
    for (int depth = 0; depth < 10; depth++) {
      Path pom =
          repo.resolve(group.replace('.', '/'))
              .resolve(artifact)
              .resolve(version)
              .resolve(artifact + "-" + version + ".pom");
      if (!Files.exists(pom)) {
        throw new IllegalStateException("no POM at " + pom);
      }
      String xml = Files.readString(pom);
      List<String> out = new ArrayList<>();
      Matcher m = LICENSE_NAME.matcher(xml);
      while (m.find()) {
        out.add(m.group(1).strip());
      }
      if (!out.isEmpty()) {
        return out;
      }
      Matcher p = PARENT.matcher(xml);
      if (!p.find()) {
        return out;
      }
      group = p.group(1).strip();
      artifact = p.group(2).strip();
      version = p.group(3).strip();
    }
    return List.of();
  }

  public static void main(String[] args) throws Exception {
    Path repo =
        Path.of(
            System.getProperty(
                "repo", Path.of(System.getProperty("user.home"), ".m2", "repository").toString()));
    List<String> lines = new ArrayList<>();
    for (String a : args) {
      lines.addAll(Files.readAllLines(Path.of(a)));
    }
    int checked = 0;
    int bad = 0;
    for (String line : lines) {
      Matcher m = ARTIFACT.matcher(line);
      if (!m.matches() || line.contains("The following files have been resolved")) {
        continue;
      }
      String group = m.group(1);
      String artifact = m.group(2);
      String version = m.group(3);
      String scope = m.group(4);
      List<String> names = licenses(repo, group, artifact, version);
      boolean runtime = scope.equals("compile") || scope.equals("runtime");
      boolean ok = !runtime && !names.isEmpty() && names.stream().anyMatch(CheckLicenses::allowed);
      if (runtime) {
        System.out.println(
            "runtime dependency: the client has none by design (clients/java/README.md)");
      }
      System.out.printf(
          "%-6s %s:%s:%s (%s): %s%n",
          ok ? "ok" : "DENIED",
          group,
          artifact,
          version,
          scope,
          names.isEmpty() ? "no licence" : names);
      checked++;
      if (!ok) {
        bad++;
      }
    }
    if (bad > 0) {
      System.err.println(
          bad
              + " dependencies refused: runtime-scoped, or outside MIT/BSD/Apache-2.0/ISC (ADR 0006)");
      System.exit(1);
    }
    System.out.println(
        checked + " non-test dependencies checked; runtime closure: none besides the JDK");
  }
}
