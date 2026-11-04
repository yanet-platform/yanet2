#ifndef _GNU_SOURCE
#define _GNU_SOURCE
#endif
#include "config_source.h"

#include <ctype.h>
#include <dirent.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <yaml.h>

#define INCLUDE_DEPTH 32
#define INCLUDE_FILES 256
#define INCLUDE_BYTES (16u << 20)
#define INCLUDE_NODES 100000

struct source_reader {
	yaml_document_t document;
	char *active[INCLUDE_DEPTH + 1];
	size_t files;
	size_t bytes;
	size_t nodes;
};

struct source_input {
	FILE *file;
	struct source_reader *reader;
};

static int
read_input(void *data, unsigned char *buffer, size_t size, size_t *size_read) {
	struct source_input *input = data;
	size_t available = INCLUDE_BYTES - input->reader->bytes + 1;
	if (size > available) {
		size = available;
	}
	*size_read = fread(buffer, 1, size, input->file);
	input->reader->bytes += *size_read;
	if (input->reader->bytes > INCLUDE_BYTES) {
		fprintf(stderr, "configuration includes exceed %u bytes\n",
			INCLUDE_BYTES);
		return 0;
	}
	return !ferror(input->file);
}

static int
read_source(struct source_reader *, const char *, unsigned);

static int
empty_document(yaml_node_t *root) {
	return root == NULL ||
	       (root->type == YAML_SCALAR_NODE &&
		root->data.scalar.length == 0 &&
		root->data.scalar.style == YAML_PLAIN_SCALAR_STYLE);
}

static int
scalar_equal(yaml_node_t *left, yaml_node_t *right) {
	return left->type == YAML_SCALAR_NODE &&
	       right->type == YAML_SCALAR_NODE &&
	       left->data.scalar.length == right->data.scalar.length &&
	       !memcmp(left->data.scalar.value, right->data.scalar.value,
		       left->data.scalar.length);
}

static int
named(yaml_node_t *node, const char *name) {
	return node->type == YAML_SCALAR_NODE &&
	       node->data.scalar.length == strlen(name) &&
	       !memcmp(node->data.scalar.value, name, strlen(name));
}

static yaml_node_pair_t *
lookup(yaml_document_t *document, int mapping, yaml_node_t *key) {
	yaml_node_t *node = yaml_document_get_node(document, mapping);
	for (yaml_node_pair_t *pair = node->data.mapping.pairs.start;
	     pair != node->data.mapping.pairs.top;
	     ++pair) {
		if (scalar_equal(yaml_document_get_node(document, pair->key),
				 key)) {
			return pair;
		}
	}
	return NULL;
}

static int
merge_mapping(struct source_reader *reader, int base, int overlay) {
	yaml_node_t *node = yaml_document_get_node(&reader->document, overlay);
	for (yaml_node_pair_t *pair = node->data.mapping.pairs.start;
	     pair != node->data.mapping.pairs.top;
	     ++pair) {
		yaml_node_pair_t *existing =
			lookup(&reader->document, base,
			       yaml_document_get_node(&reader->document,
						      pair->key));
		if (existing == NULL) {
			if (!yaml_document_append_mapping_pair(
				    &reader->document, base, pair->key,
				    pair->value
			    )) {
				return 0;
			}
		} else if (yaml_document_get_node(&reader->document,
						  existing->value)
					   ->type == YAML_MAPPING_NODE &&
			   yaml_document_get_node(&reader->document,
						  pair->value)
					   ->type == YAML_MAPPING_NODE) {
			if (!merge_mapping(reader, existing->value,
					   pair->value)) {
				return 0;
			}
		} else {
			existing->value = pair->value;
		}
	}
	return 1;
}

static int
copy_node(struct source_reader *reader, yaml_document_t *source, int index,
	  unsigned depth, int root) {
	if (depth > 64 || ++reader->nodes > INCLUDE_NODES) {
		return 0;
	}
	yaml_node_t *node = yaml_document_get_node(source, index);
	if (node->type == YAML_SCALAR_NODE) {
		return yaml_document_add_scalar(&reader->document, node->tag,
						node->data.scalar.value,
						node->data.scalar.length,
						node->data.scalar.style);
	}
	int result;
	if (node->type == YAML_SEQUENCE_NODE) {
		result =
			yaml_document_add_sequence(&reader->document, node->tag,
						   node->data.sequence.style);
		if (!result) {
			return 0;
		}
		for (yaml_node_item_t *item = node->data.sequence.items.start;
		     item != node->data.sequence.items.top;
		     ++item) {
			int value =
				copy_node(reader, source, *item, depth + 1, 0);
			if (!value || !yaml_document_append_sequence_item(
					      &reader->document, result, value
				      )) {
				return 0;
			}
		}
		return result;
	}
	result = yaml_document_add_mapping(&reader->document, node->tag,
					   node->data.mapping.style);
	if (!result) {
		return 0;
	}
	for (yaml_node_pair_t *pair = node->data.mapping.pairs.start;
	     pair != node->data.mapping.pairs.top;
	     ++pair) {
		yaml_node_t *key = yaml_document_get_node(source, pair->key);
		if (key->type != YAML_SCALAR_NODE || named(key, "<<")) {
			fprintf(stderr,
				"dataplane configuration requires scalar keys "
				"and does not support YAML merge keys\n");
			return 0;
		}
		for (yaml_node_pair_t *previous =
			     node->data.mapping.pairs.start;
		     previous != pair;
		     ++previous) {
			if (scalar_equal(
				    key, yaml_document_get_node(source,
								previous->key)
			    )) {
				fprintf(stderr,
					"duplicate YAML key at line %zu\n",
					key->start_mark.line + 1);
				return 0;
			}
		}
		if (root && named(key, "include")) {
			continue;
		}
		int copied_key =
			copy_node(reader, source, pair->key, depth + 1, 0);
		int value =
			copy_node(reader, source, pair->value, depth + 1, 0);
		if (!copied_key || !value ||
		    !yaml_document_append_mapping_pair(&reader->document,
						       result, copied_key,
						       value)) {
			return 0;
		}
	}
	return result;
}

static char *
join_path(const char *directory, const char *name) {
	char *result = NULL;
	if (asprintf(&result, "%s/%s", directory, name) < 0) {
		return NULL;
	}
	return result;
}

static int
read_file(struct source_reader *reader, const char *path, unsigned depth) {
	FILE *file = fopen(path, "r");
	if (!file) {
		return 0;
	}
	yaml_parser_t parser;
	if (!yaml_parser_initialize(&parser)) {
		fclose(file);
		return 0;
	}
	struct source_input input = {file, reader};
	yaml_parser_set_input(&parser, read_input, &input);
	yaml_document_t source;
	int result = 0;
	if (!yaml_parser_load(&parser, &source)) {
		fprintf(stderr, "%s:%zu: %s\n", path,
			parser.problem_mark.line + 1, parser.problem);
		goto done;
	}
	yaml_node_t *root = yaml_document_get_root_node(&source);
	if (empty_document(root)) {
		result = yaml_document_add_mapping(&reader->document, NULL,
						   YAML_BLOCK_MAPPING_STYLE);
		goto source_done;
	}
	if (root->type != YAML_MAPPING_NODE) {
		goto source_done;
	}
	int local = copy_node(reader, &source, 1, 0, 1);
	int merged = yaml_document_add_mapping(&reader->document, NULL,
					       YAML_BLOCK_MAPPING_STYLE);
	if (!local || !merged) {
		goto source_done;
	}
	for (yaml_node_pair_t *pair = root->data.mapping.pairs.start;
	     pair != root->data.mapping.pairs.top;
	     ++pair) {
		if (!named(yaml_document_get_node(&source, pair->key),
			   "include")) {
			continue;
		}
		yaml_node_t *include =
			yaml_document_get_node(&source, pair->value);
		if (include->type != YAML_SEQUENCE_NODE) {
			goto source_done;
		}
		for (yaml_node_item_t *item =
			     include->data.sequence.items.start;
		     item != include->data.sequence.items.top;
		     ++item) {
			yaml_node_t *entry =
				yaml_document_get_node(&source, *item);
			if (entry->type != YAML_SCALAR_NODE ||
			    !entry->data.scalar.length ||
			    strlen((char *)entry->data.scalar.value) !=
				    entry->data.scalar.length) {
				goto source_done;
			}
			char *directory = strdup(path);
			if (!directory) {
				goto source_done;
			}
			char *slash = strrchr(directory, '/');
			if (slash) {
				*slash = '\0';
			} else {
				strcpy(directory, ".");
			}
			const char *name = (char *)entry->data.scalar.value;
			const char *nonspace = name;
			while (isspace((unsigned char)*nonspace)) {
				++nonspace;
			}
			if (*nonspace == '\0') {
				free(directory);
				goto source_done;
			}
			char *included = name[0] == '/'
						 ? strdup(name)
						 : join_path(directory, name);
			free(directory);
			int fragment = included ? read_source(reader, included,
							      depth + 1)
						: 0;
			free(included);
			if (!fragment ||
			    !merge_mapping(reader, merged, fragment)) {
				goto source_done;
			}
		}
	}
	if (merge_mapping(reader, merged, local)) {
		result = merged;
	}
source_done:
	yaml_document_delete(&source);
	while (result && !parser.stream_end_produced) {
		yaml_document_t extra;
		if (!yaml_parser_load(&parser, &extra)) {
			result = 0;
		} else {
			if (!empty_document(yaml_document_get_root_node(&extra)
			    )) {
				result = 0;
			}
			yaml_document_delete(&extra);
		}
	}
done:
	yaml_parser_delete(&parser);
	fclose(file);
	return result;
}

static int
entry_order(const struct dirent **left, const struct dirent **right) {
	return strcmp((*left)->d_name, (*right)->d_name);
}

static int
read_directory(struct source_reader *reader, const char *path, unsigned depth) {
	struct dirent **entries;
	int count = scandir(path, &entries, NULL, entry_order);
	if (count < 0) {
		return 0;
	}
	size_t files_before = reader->files;
	int merged = yaml_document_add_mapping(&reader->document, NULL,
					       YAML_BLOCK_MAPPING_STYLE);
	for (int idx = 0; idx < count; ++idx) {
		if (merged && entries[idx]->d_name[0] != '.') {
			char *name = join_path(path, entries[idx]->d_name);
			struct stat info;
			if (!name || stat(name, &info)) {
				merged = 0;
			} else {
				const char *extension = strrchr(name, '.');
				if (S_ISDIR(info.st_mode) ||
				    (extension &&
				     (!strcmp(extension, ".yaml") ||
				      !strcmp(extension, ".yml")))) {
					int fragment = read_source(reader, name,
								   depth + 1);
					if (!fragment ||
					    !merge_mapping(reader, merged,
							   fragment)) {
						merged = 0;
					}
				}
			}
			free(name);
		}
		free(entries[idx]);
	}
	free(entries);
	return reader->files == files_before ? 0 : merged;
}

static int
read_source(struct source_reader *reader, const char *path, unsigned depth) {
	if (depth > INCLUDE_DEPTH) {
		fprintf(stderr, "%s: include depth exceeds %d\n", path,
			INCLUDE_DEPTH);
		return 0;
	}
	char *canonical = realpath(path, NULL);
	if (!canonical) {
		perror(path);
		return 0;
	}
	for (unsigned idx = 0; idx < depth; ++idx) {
		if (!strcmp(reader->active[idx], canonical)) {
			fprintf(stderr, "%s: include cycle\n", path);
			free(canonical);
			return 0;
		}
	}
	reader->active[depth] = canonical;
	struct stat info;
	int result = 0;
	if (stat(path, &info) == 0) {
		if (S_ISDIR(info.st_mode)) {
			result = read_directory(reader, path, depth);
		} else if (S_ISREG(info.st_mode) &&
			   ++reader->files <= INCLUDE_FILES) {
			result = read_file(reader, path, depth);
		}
	}
	free(canonical);
	reader->active[depth] = NULL;
	if (!result) {
		fprintf(stderr, "%s: invalid configuration include\n", path);
	}
	return result;
}

FILE *
dataplane_config_open(const char *path) {
	struct source_reader reader = {0};
	if (!yaml_document_initialize(&reader.document, NULL, NULL, NULL, 1,
				      1)) {
		return NULL;
	}
	// The first node remains the document root while sources are composed.
	int root = yaml_document_add_mapping(&reader.document, NULL,
					     YAML_BLOCK_MAPPING_STYLE);
	int merged = root ? read_source(&reader, path, 0) : 0;
	if (!merged || !merge_mapping(&reader, root, merged)) {
		yaml_document_delete(&reader.document);
		return NULL;
	}
	FILE *file = tmpfile();
	yaml_emitter_t emitter;
	if (!file || !yaml_emitter_initialize(&emitter)) {
		yaml_document_delete(&reader.document);
		if (file) {
			fclose(file);
		}
		return NULL;
	}
	yaml_emitter_set_output_file(&emitter, file);
	int success = yaml_emitter_dump(&emitter, &reader.document);
	yaml_emitter_delete(&emitter);
	if (!success || fflush(file) || fseek(file, 0, SEEK_SET)) {
		fclose(file);
		return NULL;
	}
	return file;
}
