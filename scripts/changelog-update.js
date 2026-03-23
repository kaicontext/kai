// @trigger schedule
// @startDateTime 2026-02-19T09:00:00
// @rrule FREQ=DAILY
// @tz America/New_York
// @title Kai Changelog Update

const REPO = "kaicontext/kai";
const HEADER = "# Changelog\n\nAll notable changes to Kai are documented here.\n\n";

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

  // Build new section
  const today = new Date().toISOString().split("T")[0];
  const lines = [];
  lines.push(`## ${today} — since ${lastTag}`);
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

  const newSection = lines.join("\n");
  console.log(newSection);

  // Fetch existing CHANGELOG.md to accumulate
  const ghToken = secrets.get("GITHUB_TOKEN");
  const apiBase = `https://api.github.com/repos/${REPO}/contents/CHANGELOG.md`;
  const apiHeaders = {
    Authorization: `Bearer ${ghToken}`,
    Accept: "application/vnd.github.v3+json",
  };

  let existingContent = "";
  let fileSha = null;
  try {
    const existing = await http.get(apiBase, { headers: apiHeaders });
    fileSha = existing.data.sha;
    existingContent = atob(existing.data.content.replace(/\n/g, ""));
  } catch (e) {
    console.log("CHANGELOG.md does not exist yet, will create it.");
  }

  // Strip the header from existing content to get previous sections
  let previousSections = existingContent;
  if (previousSections.startsWith("# Changelog")) {
    // Remove everything up to the first ## section
    const firstSection = previousSections.indexOf("\n## ");
    if (firstSection !== -1) {
      previousSections = previousSections.substring(firstSection + 1);
    } else {
      previousSections = "";
    }
  }

  // Don't duplicate if this section already exists
  if (previousSections.includes(`## ${today} — since ${lastTag}`)) {
    // Replace the existing section for today
    const sectionStart = previousSections.indexOf(`## ${today} — since ${lastTag}`);
    const nextSection = previousSections.indexOf("\n## ", sectionStart + 1);
    if (nextSection !== -1) {
      previousSections = previousSections.substring(nextSection + 1);
    } else {
      previousSections = "";
    }
  }

  // Assemble: header + new section + previous sections
  const fullChangelog = HEADER + newSection + (previousSections ? "\n" + previousSections : "");

  const commitBody = {
    message: `Update CHANGELOG.md (${today}: ${commits.length} commits since ${lastTag})`,
    content: btoa(fullChangelog),
    branch: "main",
  };
  if (fileSha) {
    commitBody.sha = fileSha;
  }

  await http.put(apiBase, commitBody, { headers: apiHeaders });
  console.log("Committed CHANGELOG.md to main.");

  // Create a 1medium task with the changelog
  await task.create({
    title: `Changelog update: ${commits.length} commits since ${lastTag}`,
    description: newSection,
  });
}
