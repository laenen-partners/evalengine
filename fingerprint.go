package evalengine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// ComputeFingerprint hashes the values of the declared input fields from the
// given proto message. If the hash matches a cached value, the previous
// evaluation result can be reused.
func ComputeFingerprint(reads []FieldRef, msg proto.Message) string {
	if msg == nil {
		return ""
	}

	h := sha256.New()

	// Sort reads for deterministic ordering.
	sorted := make([]string, len(reads))
	for i, r := range reads {
		sorted[i] = string(r)
	}
	sort.Strings(sorted)

	ref := msg.ProtoReflect()
	var buf []byte
	for _, field := range sorted {
		// Extract the field value from the proto message by descriptor name.
		val := resolveField(ref, field)
		// Normalise the key: strip "input." prefix so that "input.score" and
		// "score" produce identical hash contributions (resolveField does the
		// same stripping, so the resolved value is already the same).
		key := field
		if len(key) > 6 && key[:6] == "input." {
			key = key[6:]
		}
		// Include %T to prevent type collisions: int(1) and string("1") must
		// not produce the same hash contribution.
		// fmt.Appendf has no error return; hash.Hash.Write is documented never to error.
		buf = fmt.Appendf(buf[:0], "%s\x00%T\x00%v\x00", key, val, val)
		h.Write(buf)
	}

	return hex.EncodeToString(h.Sum(nil))
}

// resolveField extracts a field value from the proto message given a dotted
// field reference like "input.nested_object.is_active" or "score". It walks
// through nested messages following each segment of the dot-separated path.
func resolveField(msg protoreflect.Message, ref string) any {
	// Strip "input." prefix — eval reads declarations use "input.<field>" to
	// reference fields on the proto message passed to Engine.Run.
	name := ref
	if len(name) > 6 && name[:6] == "input." {
		name = name[6:]
	}

	parts := strings.Split(name, ".")
	current := msg
	for i, part := range parts {
		desc := current.Descriptor()
		fd := desc.Fields().ByName(protoreflect.Name(part))
		if fd == nil {
			return nil
		}

		if i < len(parts)-1 {
			// Intermediate segment — must be a message to descend into.
			if fd.Kind() != protoreflect.MessageKind {
				return nil
			}
			nested := current.Get(fd).Message()
			if !nested.IsValid() {
				return nil
			}
			current = nested
			continue
		}

		// Leaf segment — extract the value.
		val := current.Get(fd)

		// For repeated fields (lists), serialize for stable hashing.
		if fd.IsList() {
			list := val.List()
			var items []string
			for j := 0; j < list.Len(); j++ {
				item := list.Get(j)
				if item.Message() != nil {
					b, _ := proto.MarshalOptions{Deterministic: true}.Marshal(item.Message().Interface())
					items = append(items, string(b))
				} else {
					items = append(items, fmt.Sprintf("%v", item.Interface()))
				}
			}
			return items
		}

		return val.Interface()
	}
	return nil
}
