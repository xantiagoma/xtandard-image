// Checks the licences of the packages' dependency closure (ADR 0006 allowlist, ADR 0013).
//
//   dotnet run scripts/check-licenses.cs      (after a restore; `task licenses`)
//
// It reads every src/*/packages.lock.json (the resolved closure, per target framework) and the
// licence expression of each package's .nuspec in the NuGet global packages folder. Allowed: MIT,
// BSD, Apache-2.0, ISC (and expressions of them). Exceptions are listed with their reason; there
// are none. Xtandard.Image has no package dependencies (only the SDK's build-time
// Microsoft.NET.ILLink.Tasks, which IsAotCompatible adds and which ships nothing at run time);
// Xtandard.Image.Extensions brings Microsoft.Extensions.Http and its Microsoft.Extensions.* closure.
#:property RestorePackagesWithLockFile=false
#:property RestoreLockedMode=false
#:property PublishAot=false

using System.Text.Json;
using System.Text.RegularExpressions;
using System.Xml.Linq;

var allowed = new Regex("^(MIT|MIT-0|BSD-2-Clause|BSD-3-Clause|Apache-2.0|ISC)$", RegexOptions.IgnoreCase);

// Packages whose licence is outside the allowlist, with the reason they are accepted.
var exceptions = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);

var root = Path.GetFullPath(Path.Combine(AppContext.GetData("EntryPointFileDirectoryPath") as string ?? ".", ".."));
var packages = Environment.GetEnvironmentVariable("NUGET_PACKAGES")
    ?? Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), ".nuget", "packages");

var closure = new SortedSet<(string Id, string Version)>();
foreach (var lockFile in Directory.GetFiles(Path.Combine(root, "src"), "packages.lock.json", SearchOption.AllDirectories))
{
    using var doc = JsonDocument.Parse(File.ReadAllText(lockFile));
    foreach (var tfm in doc.RootElement.GetProperty("dependencies").EnumerateObject())
    {
        foreach (var dep in tfm.Value.EnumerateObject())
        {
            if (dep.Value.GetProperty("type").GetString() != "Project")
            {
                closure.Add((dep.Name, dep.Value.GetProperty("resolved").GetString()!));
            }
        }
    }
}

if (closure.Count == 0)
{
    Console.Error.WriteLine("no packages.lock.json under src/: run `dotnet restore` first");
    return 1;
}

var bad = 0;
foreach (var (id, version) in closure)
{
    var nuspec = Path.Combine(packages, id.ToLowerInvariant(), version, $"{id.ToLowerInvariant()}.nuspec");
    var licence = "UNKNOWN";
    if (File.Exists(nuspec))
    {
        var meta = XDocument.Load(nuspec).Root?.Elements().FirstOrDefault(e => e.Name.LocalName == "metadata");
        var el = meta?.Elements().FirstOrDefault(e => e.Name.LocalName == "license");
        if (el is not null && (string?)el.Attribute("type") == "expression")
        {
            licence = el.Value.Trim();
        }
    }

    var terms = Regex.Split(licence, @"\s+(?:OR|AND)\s+|[()]").Select(t => t.Trim()).Where(t => t.Length > 0).ToList();
    string status;
    if (terms.Count > 0 && terms.All(t => allowed.IsMatch(t)))
    {
        status = "ok";
    }
    else if (exceptions.TryGetValue(id, out var why) && Regex.IsMatch(licence, why))
    {
        status = "exception";
    }
    else
    {
        status = "NOT ALLOWED";
        bad++;
    }

    Console.WriteLine($"{status,-12} {id} {version}: {licence}");
}

return bad > 0 ? 1 : 0;
