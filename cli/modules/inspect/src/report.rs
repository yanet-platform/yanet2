use std::collections::{BTreeMap, BTreeSet};

use bytesize::ByteSize;
use clap::ValueEnum;
use unicode_segmentation::UnicodeSegmentation;
use unicode_width::UnicodeWidthStr;
use ync::{display, output};
use ynpb::pb::{DevicePipelineInfo, InspectResponse, InstanceInfo, MemoryNode};

use crate::memory::{MemoryTree, node_live};

/// One block of the report, selectable on the command line.
#[derive(Debug, Clone, Copy, PartialEq, Eq, ValueEnum)]
pub enum Section {
    Memory,
    Device,
    Pipeline,
    Function,
    Module,
}

/// How much of the instance the report shows: which blocks, whether each
/// agent's memory contexts are expanded, and how many levels of the context
/// tree are printed.
#[derive(Debug, Clone)]
pub struct View {
    sections: Vec<Section>,
    memory: bool,
    depth: Option<usize>,
}

impl View {
    pub fn new(sections: Vec<Section>, memory: bool, depth: Option<usize>) -> Self {
        Self { sections, memory, depth }
    }

    /// Reports whether a block belongs to the report; selecting none selects
    /// them all.
    pub fn shows(&self, section: Section) -> bool {
        self.sections.is_empty() || self.sections.contains(&section)
    }

    /// Reports whether memory contexts are expanded under each agent.
    ///
    /// Asking for a depth is asking for the tree the depth applies to.
    fn expands_memory(&self) -> bool {
        self.memory || self.depth.is_some()
    }

    /// Returns how many levels of the context tree are printed.
    ///
    /// The levels are counted from the topmost printed context, and the
    /// subtree totals stay those of the whole tree, so a cut branch still
    /// accounts for everything below it.
    fn levels(&self) -> usize {
        self.depth.unwrap_or(usize::MAX)
    }
}

#[derive(Clone, Copy, Default, PartialEq, Eq)]
enum Style {
    #[default]
    Plain,
    Dim,
    Bold,
    Warning,
    Error,
}

impl Style {
    fn paint(self, text: &str, colored: bool) -> String {
        if !colored {
            return text.to_string();
        }
        match self {
            Self::Plain => text.to_string(),
            Self::Dim => output::paint_dim(text),
            Self::Bold => output::paint_bold(text),
            Self::Warning => output::paint_warning(text),
            Self::Error => output::paint_error(text),
        }
    }
}

struct Span {
    text: String,
    style: Style,
}

impl Span {
    fn new(text: impl Into<String>, style: Style) -> Self {
        Self { text: text.into(), style }
    }
}

struct Field {
    label: &'static str,
    value: String,
    style: Style,
}

impl Field {
    fn new(label: &'static str, value: impl Into<String>) -> Self {
        Self {
            label,
            value: value.into(),
            style: Style::Plain,
        }
    }

    fn width(&self) -> usize {
        self.label.width() + usize::from(!self.label.is_empty()) + self.value.width()
    }
}

struct Record {
    kind: &'static str,
    prefix: String,
    name: String,
    fields: Vec<Field>,
    tree: bool,
    dimmed: bool,
}

impl Record {
    fn new(kind: &'static str, name: impl Into<String>, fields: Vec<Field>) -> Self {
        Self {
            kind,
            prefix: String::new(),
            name: name.into(),
            fields,
            tree: false,
            dimmed: false,
        }
    }

    fn name_width(&self) -> usize {
        self.prefix.width() + self.name.width()
    }

    fn field_group(&self) -> &'static str {
        if self.tree { "memory" } else { self.kind }
    }
}

pub fn render(response: &InspectResponse, view: &View) {
    let Some(info) = &response.instance_info else {
        output::empty(format_args!("No instance information found."));
        return;
    };

    let colored = output::stdout_is_terminal() && output::is_colored();
    let mut groups = Vec::new();
    if view.shows(Section::Memory) {
        groups.push(memory_records(info, view, colored));
    }
    if view.shows(Section::Device) {
        groups.push(device_records(info));
    }
    if view.shows(Section::Pipeline) {
        groups.push(pipeline_records(info));
    }
    if view.shows(Section::Function) {
        groups.push(function_records(info));
    }
    if view.shows(Section::Module) {
        groups.push(module_records(info));
    }

    let width = display::terminal_width().filter(|&width| width > 0);
    let header = format!("YANET  instance {}  NUMA {}", info.instance_idx, info.numa_idx);
    let report = format_report(&header, &groups, width, colored);
    print!("{report}");
}

fn bytes(value: u64) -> String {
    ByteSize::b(value).display().iec().to_string()
}

/// Escaped control characters keep names from injecting terminal commands
/// or creating extra report lines.
fn name(value: &str) -> String {
    let mut escaped = String::new();
    for ch in value.chars() {
        if ch.is_control() {
            escaped.extend(ch.escape_default());
        } else {
            escaped.push(ch);
        }
    }
    escaped
}

fn list(values: impl IntoIterator<Item = String>, separator: &str) -> String {
    let values: Vec<_> = values.into_iter().collect();
    if values.is_empty() {
        "-".into()
    } else {
        values.join(separator)
    }
}

fn nonempty(records: Vec<Record>, kind: &'static str) -> Vec<Record> {
    if records.is_empty() {
        vec![Record::new(kind, "-", vec![Field::new("", "none")])]
    } else {
        records
    }
}

fn memory_records(info: &InstanceInfo, view: &View, unicode: bool) -> Vec<Record> {
    let details = view.expands_memory();
    let mut agents: Vec<_> = info.agents.iter().collect();
    agents.sort_by_key(|agent| &agent.name);
    let mut rows = Vec::new();
    for agent in agents {
        if agent.instances.is_empty() {
            let mut fields = if details {
                memory_fields("-".into(), "-".into(), false)
            } else {
                Vec::new()
            };
            fields.push(Field::new("instances", "0"));
            rows.push(Record::new("memory", name(&agent.name), fields));
        }
        let mut instances: Vec<_> = agent.instances.iter().collect();
        instances.sort_by_key(|instance| (instance.pid, instance.generation));
        for instance in instances {
            let used = instance.memory_limit.saturating_sub(instance.free_bytes);
            let mut usage = Field::new("", "-");
            if instance.memory_limit > 0 {
                let percent = u128::from(used) * 100 / u128::from(instance.memory_limit);
                usage.value = format!("{percent}%");
                usage.style = match percent {
                    95.. => Style::Error,
                    80.. => Style::Warning,
                    _ => Style::Plain,
                };
                if percent >= 80 {
                    usage.value.push_str(" !");
                }
            }
            let row = Record::new(
                "memory",
                name(&agent.name),
                vec![
                    Field::new("pid", instance.pid.to_string()),
                    Field::new("gen", instance.generation.to_string()),
                    Field::new("used", format!("{} / {}", bytes(used), bytes(instance.memory_limit))),
                    Field::new("free", bytes(instance.free_bytes)),
                    usage,
                ],
            );
            if details {
                rows.extend(memory_tree_records(
                    row,
                    &agent.name,
                    &instance.memory_tree,
                    unicode,
                    view.levels(),
                ));
            } else {
                rows.push(row);
            }
        }
    }
    nonempty(rows, "memory")
}

fn bindings(pipelines: &[DevicePipelineInfo]) -> String {
    list(
        pipelines
            .iter()
            .map(|pipeline| format!("{} (w={})", name(&pipeline.name), pipeline.weight)),
        ", ",
    )
}

fn device_records(info: &InstanceInfo) -> Vec<Record> {
    let mut devices: Vec<_> = info.devices.iter().collect();
    devices.sort_by_key(|device| (device.index, &device.r#type, &device.name));
    nonempty(
        devices
            .into_iter()
            .map(|device| {
                Record::new(
                    "device",
                    format!("#{} {}:{}", device.index, name(&device.r#type), name(&device.name)),
                    vec![
                        Field::new("input", bindings(&device.input_pipelines)),
                        Field::new("output", bindings(&device.output_pipelines)),
                    ],
                )
            })
            .collect(),
        "device",
    )
}

fn pipeline_records(info: &InstanceInfo) -> Vec<Record> {
    let mut pipelines: Vec<_> = info.pipelines.iter().collect();
    pipelines.sort_by_key(|pipeline| &pipeline.name);
    nonempty(
        pipelines
            .into_iter()
            .map(|pipeline| {
                Record::new(
                    "pipeline",
                    name(&pipeline.name),
                    vec![Field::new("", list(pipeline.functions.iter().map(|f| name(f)), " -> "))],
                )
            })
            .collect(),
        "pipeline",
    )
}

fn function_records(info: &InstanceInfo) -> Vec<Record> {
    let mut functions: Vec<_> = info.functions.iter().collect();
    functions.sort_by_key(|function| &function.name);
    let mut rows = Vec::new();
    for function in functions {
        if function.chains.is_empty() {
            rows.push(Record::new(
                "function",
                name(&function.name),
                vec![Field::new("chains", "-")],
            ));
        }
        let mut chains: Vec<_> = function.chains.iter().collect();
        chains.sort_by_key(|chain| &chain.name);
        for chain in chains {
            let mut row = Record::new(
                "function",
                name(&function.name),
                vec![
                    Field::new("chain", name(&chain.name)),
                    Field::new("", format!("w={}", chain.weight)),
                    Field::new(
                        "",
                        list(
                            chain
                                .modules
                                .iter()
                                .map(|module| format!("{}:{}", name(&module.r#type), name(&module.name))),
                            " -> ",
                        ),
                    ),
                ],
            );
            if chain.weight == 0 {
                row.dimmed = true;
                row.fields.push(Field::new("", "[disabled]"));
            }
            rows.push(row);
        }
    }
    nonempty(rows, "function")
}

fn module_records(info: &InstanceInfo) -> Vec<Record> {
    let mut configs: BTreeMap<&str, Vec<_>> = BTreeMap::new();
    for config in &info.cp_configs {
        configs.entry(&config.r#type).or_default().push(config);
    }
    for group in configs.values_mut() {
        group.sort_by_key(|config| (&config.name, config.generation));
    }
    let loaded: BTreeSet<_> = info.dp_modules.iter().map(|module| module.name.as_str()).collect();
    let mut rows: Vec<_> = info
        .dp_modules
        .iter()
        .enumerate()
        .map(|(idx, module)| {
            let values = configs
                .get(module.name.as_str())
                .into_iter()
                .flatten()
                .map(|config| format!("{} (gen {})", name(&config.name), config.generation));
            Record::new(
                "module",
                name(&module.name),
                vec![
                    Field::new("id", idx.to_string()),
                    Field::new("configs", list(values, ", ")),
                ],
            )
        })
        .collect();
    for (kind, configs) in configs {
        if !loaded.contains(kind) {
            for config in configs {
                rows.push(Record::new(
                    "config",
                    format!("{}:{}", name(kind), name(&config.name)),
                    vec![Field::new("gen", config.generation.to_string())],
                ));
            }
        }
    }
    nonempty(rows, "module")
}

fn memory_fields(total: String, own: String, branch: bool) -> Vec<Field> {
    let mut total = Field::new("total", total);
    total.style = if branch { Style::Bold } else { Style::Plain };
    vec![total, Field::new("own", own)]
}

fn memory_tree_records(
    mut root: Record,
    agent_name: &str,
    nodes: &[MemoryNode],
    unicode: bool,
    levels: usize,
) -> Vec<Record> {
    let tree = MemoryTree::new(nodes);
    let folded_root = match tree.roots.as_slice() {
        &[idx] if nodes[idx].parent_idx == u32::MAX && nodes[idx].name == agent_name => Some(idx),
        _ => None,
    };
    if nodes.is_empty() {
        root.name.push_str(" [no contexts]");
    }
    let total = tree
        .roots
        .iter()
        .fold(0u64, |sum, &idx| sum.saturating_add(tree.totals[idx]));
    root.fields.splice(
        0..0,
        memory_fields(
            if nodes.is_empty() { "-".into() } else { bytes(total) },
            folded_root
                .map(|idx| bytes(node_live(&nodes[idx])))
                .unwrap_or_else(|| "-".into()),
            true,
        ),
    );
    let mut rows = vec![root];

    let roots = folded_root.map(|idx| &tree.children[idx]).unwrap_or(&tree.roots);
    let mut pending: Vec<(usize, usize, bool)> = Vec::new();
    if levels > 0 {
        pending.extend(sibling_level(roots, 0));
    }
    let mut continuation = Vec::new();
    while let Some((idx, depth, last)) = pending.pop() {
        continuation.truncate(depth);
        let node = &nodes[idx];
        let mut label = name(&node.name);
        if node.parent_idx != u32::MAX && node.parent_idx as usize >= idx {
            label.push_str(&format!(" [invalid parent #{}]", node.parent_idx));
        }
        let children = &tree.children[idx];
        let mut row = Record::new(
            "",
            label,
            memory_fields(bytes(tree.totals[idx]), bytes(node_live(node)), !children.is_empty()),
        );
        row.tree = true;
        for &more in &continuation {
            row.prefix.push_str(if more {
                if unicode { "│  " } else { "|  " }
            } else {
                "   "
            });
        }
        row.prefix.push_str(match (unicode, last) {
            (true, false) => "├─ ",
            (true, true) => "└─ ",
            (false, false) => "|- ",
            (false, true) => "`- ",
        });
        rows.push(row);
        continuation.push(!last);
        if depth + 1 < levels {
            pending.extend(sibling_level(children, depth + 1));
        }
    }
    rows
}

/// Returns one level of the traversal stack: each sibling with its depth and
/// whether it closes the level, ordered so that popping visits them in turn.
fn sibling_level(siblings: &[usize], depth: usize) -> impl Iterator<Item = (usize, usize, bool)> + '_ {
    siblings
        .iter()
        .enumerate()
        .rev()
        .map(move |(pos, &idx)| (idx, depth, pos + 1 == siblings.len()))
}

fn format_report(header: &str, groups: &[Vec<Record>], width: Option<usize>, colored: bool) -> String {
    let limit = width.unwrap_or(usize::MAX);
    let kind_width = groups.iter().flatten().map(|row| row.kind.width()).max().unwrap_or(0);
    let mut name_width = groups
        .iter()
        .flatten()
        .filter(|row| !row.tree)
        .map(Record::name_width)
        .max()
        .unwrap_or(0)
        .min((limit / 3).min(40));
    let mut field_widths: BTreeMap<&str, Vec<usize>> = BTreeMap::new();
    for row in groups.iter().flatten() {
        let widths = field_widths.entry(row.field_group()).or_default();
        widths.resize(widths.len().max(row.fields.len()), 0);
        for (width, field) in widths.iter_mut().zip(&row.fields) {
            *width = (*width).max(field.width());
        }
    }
    if let Some(widths) = field_widths.get("memory") {
        let widths = &widths[..widths.len().min(2)];
        let values_width = widths.iter().sum::<usize>() + widths.len().saturating_sub(1) * 2;
        let available = limit.saturating_sub(kind_width + 4 + values_width);
        name_width = name_width.max(
            groups
                .iter()
                .flatten()
                .filter(|row| row.tree)
                .map(Record::name_width)
                .max()
                .unwrap_or(0)
                .min(available),
        );
    }
    let detail_indent = kind_width + name_width + 4;
    let mut rendered_groups = Vec::new();
    let mut longest = header.width().min(limit);

    for group in groups {
        let mut lines = Vec::new();
        for row in group {
            let compact = limit < 80 && (row.field_group() != "memory" || name_width < 8);
            let widths = &field_widths[row.field_group()];
            // Memory roots and children share their leading columns even when
            // process details wrap onto another line.
            let aligned_widths = if row.field_group() == "memory" {
                &widths[..widths.len().min(2)]
            } else {
                widths
            };
            let align = !compact
                && detail_indent
                    .saturating_add(aligned_widths.iter().sum::<usize>())
                    .saturating_add(aligned_widths.len().saturating_sub(1) * 2)
                    <= limit;
            let value_style = if row.dimmed { Style::Dim } else { Style::Plain };
            let mut prefix = vec![
                Span::new(format!("{:<kind_width$}  ", row.kind), Style::Dim),
                Span::new(&row.prefix, Style::Dim),
                Span::new(&row.name, value_style),
            ];
            let mut fields = Vec::new();
            for (idx, field) in row.fields.iter().enumerate() {
                let mut spans = Vec::new();
                if !field.label.is_empty() {
                    spans.push(Span::new(format!("{} ", field.label), Style::Dim));
                }
                spans.push(Span::new(
                    &field.value,
                    if row.dimmed { Style::Dim } else { field.style },
                ));
                if idx + 1 < row.fields.len() {
                    let padding = if align {
                        widths[idx].saturating_sub(field.width()) + 2
                    } else {
                        2
                    };
                    spans.push(Span::new(" ".repeat(padding), Style::Plain));
                }
                fields.push(spans);
            }
            if compact || row.name_width() > name_width {
                lines.extend(wrap(&[prefix], limit, (kind_width + 2).min(limit / 4)));
                let indent = if compact {
                    (kind_width + 2).min(limit / 4)
                } else {
                    detail_indent
                };
                let mut details = vec![vec![Span::new(" ".repeat(indent), Style::Plain)]];
                details.extend(fields);
                lines.extend(wrap(&details, limit, indent));
            } else {
                prefix.push(Span::new(" ".repeat(name_width - row.name_width() + 2), Style::Plain));
                let mut blocks = vec![prefix];
                blocks.extend(fields);
                lines.extend(wrap(&blocks, limit, detail_indent));
            }
        }
        longest = longest.max(
            lines
                .iter()
                .map(|line| line.iter().map(|s| s.text.width()).sum())
                .max()
                .unwrap_or(0),
        );
        rendered_groups.push(lines);
    }

    let mut out = String::new();
    append_lines(
        &mut out,
        wrap(&[vec![Span::new(header, Style::Bold)]], limit, 0),
        colored,
    );
    let separator = if colored { "─" } else { "-" }.repeat(longest.min(limit));
    for lines in rendered_groups {
        out.push_str(&Style::Dim.paint(&separator, colored));
        out.push('\n');
        append_lines(&mut out, lines, colored);
    }
    out
}

/// Wrapping measures plain graphemes before applying colour, preserving
/// long identifiers and avoiding splits inside a Unicode character cluster.
fn wrap(blocks: &[Vec<Span>], width: usize, indent: usize) -> Vec<Vec<Span>> {
    let mut lines = Vec::new();
    let mut line = Vec::new();
    let mut column = 0;
    for block in blocks {
        // Padding between fields does not force a fitting value onto a new line.
        let mut block_width = 0;
        let mut trim = true;
        for span in block.iter().rev() {
            let text = if trim {
                span.text.trim_end_matches(' ')
            } else {
                &span.text
            };
            block_width += text.width();
            trim &= text.is_empty();
        }
        if block_width <= width.saturating_sub(indent) && column > indent && column.saturating_add(block_width) > width
        {
            finish_line(&mut lines, &mut line);
            line.push(Span::new(" ".repeat(indent), Style::Plain));
            column = indent;
        }
        for span in block {
            for word in span.text.split_inclusive(' ') {
                let word_width = word.trim_end_matches(' ').width();
                if column > indent && column.saturating_add(word_width) > width {
                    finish_line(&mut lines, &mut line);
                    line.push(Span::new(" ".repeat(indent), Style::Plain));
                    column = indent;
                }
                for grapheme in word.graphemes(true) {
                    let size = grapheme.width();
                    if grapheme == " " && column >= width {
                        continue;
                    }
                    if column.saturating_add(size) > width && column > indent {
                        finish_line(&mut lines, &mut line);
                        line.push(Span::new(" ".repeat(indent), Style::Plain));
                        column = indent;
                    }
                    if let Some(last) = line.last_mut().filter(|last| last.style == span.style) {
                        last.text.push_str(grapheme);
                    } else {
                        line.push(Span::new(grapheme, span.style));
                    }
                    column += size;
                }
            }
        }
    }
    finish_line(&mut lines, &mut line);
    lines
}

fn finish_line(lines: &mut Vec<Vec<Span>>, line: &mut Vec<Span>) {
    while let Some(last) = line.last_mut() {
        last.text.truncate(last.text.trim_end_matches(' ').len());
        if last.text.is_empty() {
            line.pop();
        } else {
            break;
        }
    }
    if !line.is_empty() {
        lines.push(core::mem::take(line));
    }
}

fn append_lines(out: &mut String, lines: Vec<Vec<Span>>, colored: bool) {
    for line in lines {
        for span in line {
            out.push_str(&span.style.paint(&span.text, colored));
        }
        out.push('\n');
    }
}

#[cfg(test)]
mod test {
    use super::*;

    /// Returns a memory context that allocated and never freed, so its live
    /// bytes are exactly what it was given.
    fn node(name: &str, parent_idx: u32, balloc_size: u64) -> MemoryNode {
        MemoryNode {
            name: name.to_string(),
            parent_idx,
            balloc_count: 0,
            bfree_count: 0,
            balloc_size,
            bfree_size: 0,
        }
    }

    /// Returns a chain of contexts under an agent's own root, one context per
    /// level, so a depth cut is visible as a shorter chain.
    fn chain() -> Vec<MemoryNode> {
        vec![
            node("agent", u32::MAX, 1),
            node("filter", 0, 100),
            node("lpm", 1, 20),
            node("values", 2, 3),
        ]
    }

    /// Returns the names printed for an agent's tree, the agent's own row
    /// first.
    fn printed(levels: usize) -> Vec<String> {
        let root = Record::new("memory", "agent", Vec::new());

        memory_tree_records(root, "agent", &chain(), true, levels)
            .into_iter()
            .map(|row| row.name)
            .collect()
    }

    #[test]
    fn test_memory_tree_records_prints_every_level_by_default() {
        assert_eq!(vec!["agent", "filter", "lpm", "values"], printed(usize::MAX));
    }

    #[test]
    fn test_memory_tree_records_depth_caps_printed_levels() {
        assert_eq!(vec!["agent", "filter"], printed(1));
        assert_eq!(vec!["agent", "filter", "lpm"], printed(2));
    }

    #[test]
    fn test_memory_tree_records_zero_depth_prints_no_contexts() {
        assert_eq!(vec!["agent"], printed(0));
    }

    #[test]
    fn test_memory_tree_records_cut_branch_keeps_its_whole_subtree_total() {
        let root = Record::new("memory", "agent", Vec::new());
        let rows = memory_tree_records(root, "agent", &chain(), true, 1);

        let total = &rows[1].fields[0];

        assert_eq!("total", total.label);
        assert_eq!("123 B", total.value);
    }

    #[test]
    fn test_view_without_selection_shows_every_section() {
        let view = View::new(Vec::new(), false, None);

        assert!(view.shows(Section::Memory));
        assert!(view.shows(Section::Device));
        assert!(view.shows(Section::Pipeline));
        assert!(view.shows(Section::Function));
        assert!(view.shows(Section::Module));
    }

    #[test]
    fn test_view_shows_only_the_selected_sections() {
        let view = View::new(vec![Section::Memory, Section::Module], false, None);

        assert!(view.shows(Section::Memory));
        assert!(view.shows(Section::Module));
        assert!(!view.shows(Section::Device));
    }

    #[test]
    fn test_view_depth_expands_memory_contexts() {
        assert!(!View::new(Vec::new(), false, None).expands_memory());
        assert!(View::new(Vec::new(), true, None).expands_memory());
        assert!(View::new(Vec::new(), false, Some(1)).expands_memory());
    }
}
