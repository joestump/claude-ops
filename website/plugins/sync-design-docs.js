/**
 * Docusaurus plugin that syncs ADRs and OpenSpec specs from the project's
 * canonical source directories into the website docs tree at build time.
 *
 * Source:  docs/adrs/ADR-XXXX-*.md             → website/docs/adrs/adr-XXXX.md
 * Source:  docs/openspec/specs/{name}/spec.md   → website/docs/specs/{name}/spec.md
 *          docs/openspec/specs/{name}/design.md → website/docs/specs/{name}/design.md
 *
 * Each spec gets an expandable sidebar category with separate Spec and Design pages.
 * Index files for both sections are auto-generated.
 * Generated files are .gitignored — only this plugin is committed.
 */

const fs = require('fs');
const path = require('path');

/** Extract the first H1 from markdown content. */
function extractTitle(content) {
  const match = content.match(/^#\s+(.+)$/m);
  return match ? match[1].trim() : null;
}

/** Strip YAML frontmatter (--- ... ---) from the start of content. */
function stripFrontmatter(content) {
  const match = content.match(/^---\r?\n[\s\S]*?\r?\n---\r?\n?/);
  return match ? content.slice(match[0].length) : content;
}

/** Derive a short sidebar label from an ADR/Spec title like "ADR-0001: Foo Bar" → "Foo Bar". */
function deriveSidebarLabel(title) {
  const match = title.match(/^(?:ADR|SPEC)-\d+:\s*(.+)$/);
  return match ? match[1].trim() : title;
}

/** Parse ADR number from filename like "ADR-0013-foo-bar.md" → 13 */
function parseADRNumber(filename) {
  const match = filename.match(/^ADR-(\d+)/);
  return match ? parseInt(match[1], 10) : null;
}

function syncADRs(siteDir) {
  const projectRoot = path.resolve(siteDir, '..');
  const srcDir = path.join(projectRoot, 'docs', 'adrs');
  const destDir = path.join(siteDir, 'docs', 'adrs');

  if (!fs.existsSync(srcDir)) return;
  fs.mkdirSync(destDir, {recursive: true});

  const files = fs.readdirSync(srcDir)
    .filter(f => f.match(/^ADR-\d+-.*\.md$/))
    .sort();

  const entries = [];

  for (const file of files) {
    const num = parseADRNumber(file);
    if (num === null) continue;

    const content = fs.readFileSync(path.join(srcDir, file), 'utf-8');
    const title = extractTitle(content);
    if (!title) continue;

    const label = deriveSidebarLabel(title);
    const stripped = stripFrontmatter(content);
    const destFile = `adr-${String(num).padStart(4, '0')}.md`;

    const output = [
      '---',
      `sidebar_position: ${num + 1}`,
      `sidebar_label: "${label}"`,
      '---',
      '',
      stripped,
    ].join('\n');

    fs.writeFileSync(path.join(destDir, destFile), output);
    entries.push({num, destFile: destFile.replace('.md', ''), title, label});
  }

  // Generate index.md
  const indexRows = entries
    .sort((a, b) => a.num - b.num)
    .map(e => `| [ADR-${String(e.num).padStart(4, '0')}](${e.destFile}) | ${e.label} |`)
    .join('\n');

  const index = [
    '---',
    'sidebar_position: 1',
    'sidebar_label: Overview',
    '---',
    '',
    '# Architecture Decision Records',
    '',
    'Architecture Decision Records (ADRs) capture the key architectural decisions made during the development of Claude Ops. Each ADR documents the context, decision drivers, considered options, and the chosen approach with its trade-offs.',
    '',
    '| ADR | Decision |',
    '|-----|----------|',
    indexRows,
    '',
  ].join('\n');

  fs.writeFileSync(path.join(destDir, 'index.md'), index);
}

function syncSpecs(siteDir) {
  const projectRoot = path.resolve(siteDir, '..');
  const srcDir = path.join(projectRoot, 'docs', 'openspec', 'specs');
  const destDir = path.join(siteDir, 'docs', 'specs');

  if (!fs.existsSync(srcDir)) return;
  fs.mkdirSync(destDir, {recursive: true});

  const dirs = fs.readdirSync(srcDir, {withFileTypes: true})
    .filter(d => d.isDirectory())
    .map(d => d.name)
    .sort();

  const entries = [];
  let position = 1;

  for (const dir of dirs) {
    const specPath = path.join(srcDir, dir, 'spec.md');
    const designPath = path.join(srcDir, dir, 'design.md');

    if (!fs.existsSync(specPath)) continue;

    const specContent = fs.readFileSync(specPath, 'utf-8');
    const specTitle = extractTitle(specContent);
    if (!specTitle) continue;

    const label = deriveSidebarLabel(specTitle);
    const strippedSpec = stripFrontmatter(specContent);

    position++;
    const specDestDir = path.join(destDir, dir);
    fs.mkdirSync(specDestDir, {recursive: true});

    // _category_.json controls the expandable sidebar category
    fs.writeFileSync(path.join(specDestDir, '_category_.json'), JSON.stringify({
      label,
      position,
      collapsible: true,
      collapsed: true,
    }, null, 2));

    // spec.md — the requirements document
    const specOutput = [
      '---',
      'sidebar_position: 1',
      'sidebar_label: Specification',
      '---',
      '',
      strippedSpec,
    ].join('\n');
    fs.writeFileSync(path.join(specDestDir, 'spec.md'), specOutput);

    // design.md — the architecture/design document (if it exists)
    const hasDesign = fs.existsSync(designPath);
    if (hasDesign) {
      const designContent = fs.readFileSync(designPath, 'utf-8');
      const strippedDesign = stripFrontmatter(designContent);
      const designOutput = [
        '---',
        'sidebar_position: 2',
        'sidebar_label: Design',
        '---',
        '',
        strippedDesign,
      ].join('\n');
      fs.writeFileSync(path.join(specDestDir, 'design.md'), designOutput);
    }

    entries.push({dir, label, hasDesign});
  }

  // Generate top-level index.md
  const indexRows = entries
    .map(e => {
      const specLink = `[Spec](${e.dir}/spec)`;
      const designLink = e.hasDesign ? ` / [Design](${e.dir}/design)` : '';
      return `| ${e.label} | ${specLink}${designLink} |`;
    })
    .join('\n');

  const index = [
    '---',
    'sidebar_position: 1',
    'sidebar_label: Overview',
    '---',
    '',
    '# Specifications',
    '',
    'OpenSpec specifications define the detailed requirements and design for each component of Claude Ops. Each spec includes an RFC 2119 requirements document and an architecture design document.',
    '',
    '| Component | Documents |',
    '|-----------|-----------|',
    indexRows,
    '',
  ].join('\n');

  fs.writeFileSync(path.join(destDir, 'index.md'), index);
}

module.exports = function syncDesignDocsPlugin(context) {
  // Run synchronously during plugin initialization, before docs plugin scans files
  syncADRs(context.siteDir);
  syncSpecs(context.siteDir);

  return {
    name: 'sync-design-docs',
  };
};
