package operator

import (
	"fmt"
	"os"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// LoadDescriptorSet reads a module's descriptor set: a FileDescriptorSet in
// the protobuf binary encoding, as protoc writes it with
// --descriptor_set_out.
//
// A file of the set resolves its imports in the set first, then among the
// files linked into this binary, so a set written without
// --include_imports serves as well as a full one.
func LoadDescriptorSet(path string) (*protoregistry.Files, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read descriptor set %q: %w", path, err)
	}
	set := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(data, set); err != nil {
		return nil, fmt.Errorf("failed to parse descriptor set %q: %w", path, err)
	}
	if err := addLinkedImports(set); err != nil {
		return nil, fmt.Errorf("descriptor set %q: %w", path, err)
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		return nil, fmt.Errorf("descriptor set %q: %w", path, err)
	}
	return files, nil
}

// addLinkedImports appends every file the set imports but lacks, taken from
// the files linked into this binary together with their own imports.
func addLinkedImports(set *descriptorpb.FileDescriptorSet) error {
	held := map[string]bool{}
	for _, file := range set.GetFile() {
		held[file.GetName()] = true
	}
	// The loop also visits the files it appends, so their imports are
	// added in turn.
	for idx := 0; idx < len(set.File); idx++ {
		file := set.File[idx]
		for _, dependency := range file.GetDependency() {
			if held[dependency] {
				continue
			}
			linked, err := protoregistry.GlobalFiles.FindFileByPath(dependency)
			if err != nil {
				return fmt.Errorf(
					"file %q imports %q, which is neither in the set nor linked into this binary",
					file.GetName(), dependency,
				)
			}
			set.File = append(set.File, protodesc.ToFileDescriptorProto(linked))
			held[dependency] = true
		}
	}
	return nil
}
