// @trigger schedule
// @startDateTime 2026-02-18T00:01:00
// @tz America/New_York
// @title Kai Changelog Update

const REPO = "kailayerhq/kai";

// Get latest release to know the cutoff
const releasesRes = await http.get(`https://api.github.com/repos/${REPO}/releases?per_page=1`);
const releases = releasesRes.data;
const lastRelease = releases[0];
const since = lastRelease?.published_at || new Date(Date.now() - 7 * 86400000).toISOString();
const lastTag = lastRelease?.tag_name || "unknown";

// Get commits since last release
const commitsRes = await http.get(`https://api.github.com/repos/${REPO}/commits?since=${since}&per_page=50`);
const commits = commitsRes.data;

if (!Array.isArray(commits) || commits.length === 0) {
  console.log(`No new commits since ${lastTag} (${since})`);
} else {
  // Categorize commits
  const categories = { features: [], fixes: [], chores: [] };

  for (const c of commits) {
    const msg = c.commit.message.split("\n")[0];
    const sha = c.sha.substring(0, 8);
    const entry = `- ${msg} (\`${sha}\`)`;

    if (/^(add|feat|implement)/i.test(msg)) {
      categories.features.push(entry);
    } else if (/^(fix|patch|resolve)/i.test(msg)) {
      categories.fixes.push(entry);
    } else {
      categories.chores.push(entry);
    }
  }

  // Build changelog
  const lines = [];
  lines.push(`## Changelog since ${lastTag}`);
  lines.push(`_${commits.length} commits since ${since.split("T")[0]}_\n`);

  if (categories.features.length > 0) {
    lines.push("### Features");
    lines.push(categories.features.join("\n"));
    lines.push("");
  }
  if (categories.fixes.length > 0) {
    lines.push("### Fixes");
    lines.push(categories.fixes.join("\n"));
    lines.push("");
  }
  if (categories.chores.length > 0) {
    lines.push("### Other");
    lines.push(categories.chores.join("\n"));
    lines.push("");
  }

  const changelog = lines.join("\n");
  console.log(changelog);

  // Build full CHANGELOG.md content with header
  const fullChangelog = `# Changelog\n\nAll notable changes to Kai are documented here.\n\n${changelog}`;

  // Commit CHANGELOG.md to the repo via GitHub Contents API
  const ghToken = await secrets.get("GITHUB_TOKEN");
  const apiBase = `https://api.github.com/repos/${REPO}/contents/CHANGELOG.md`;
  const headers = {
    Authorization: `Bearer ${ghToken}`,
    Accept: "application/vnd.github.v3+json",
  };

  // Get current file SHA (if it exists) for the update
  let fileSha = null;
  try {
    const existing = await http.get(apiBase, { headers });
    fileSha = existing.data.sha;
  } catch (e) {
    // File doesn't exist yet — that's fine, we'll create it
    console.log("CHANGELOG.md does not exist yet, will create it.");
  }

  const commitBody = {
    message: `Update CHANGELOG.md (${commits.length} commits since ${lastTag})`,
    content: btoa(fullChangelog),
    branch: "main",
  };
  if (fileSha) {
    commitBody.sha = fileSha;
  }

  await http.put(apiBase, commitBody, { headers });
  console.log("Committed CHANGELOG.md to main.");

  // Create a 1medium task with the changelog
  await task.create({
    title: `Changelog update: ${commits.length} commits since ${lastTag}`,
    description: changelog,
  });
}
