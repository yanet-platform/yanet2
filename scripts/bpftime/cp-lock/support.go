//go:build cp_lock_bpftime

package main

import (
	"bytes"
	"context"
	"debug/dwarf"
	"debug/elf"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func startTime(pid int) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	generation, err := parseStartTime(data, err, syscall.Kill(pid, 0) == syscall.ESRCH)
	if err != nil || generation == 0 {
		return generation, err
	}
	fields := strings.Fields(string(data[bytes.LastIndexByte(data, ')')+1:]))
	if fields[0] == "Z" || fields[0] == "X" {
		tasks, err := os.ReadDir(fmt.Sprintf("/proc/%d/task", pid))
		if err != nil {
			return 0, fmt.Errorf("cannot inspect CP task group: %w", err)
		}
		if len(tasks) == 1 && tasks[0].Name() == strconv.Itoa(pid) {
			return 0, nil
		}
	}
	return generation, nil
}

func parseStartTime(data []byte, readError error, verifiedGone bool) (uint64, error) {
	if readError != nil {
		if verifiedGone {
			return 0, nil
		}
		return 0, fmt.Errorf("cannot inspect CP generation: %w", readError)
	}
	end := bytes.LastIndexByte(data, ')')
	if end < 0 {
		return 0, fmt.Errorf("invalid CP generation")
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 20 {
		return 0, fmt.Errorf("incomplete CP generation")
	}
	return strconv.ParseUint(fields[19], 10, 64)
}

func control(pid int, request string) (bool, error) {
	connection, err := net.DialTimeout("unix", fmt.Sprintf("@bpftime-agent-%d", pid), 5*time.Second)
	if err != nil {
		return false, err
	}
	defer connection.Close()
	descriptor, err := connection.(*net.UnixConn).SyscallConn()
	if err != nil {
		return false, err
	}
	var peer *syscall.Ucred
	var peerError error
	err = descriptor.Control(func(descriptor uintptr) {
		peer, peerError = syscall.GetsockoptUcred(int(descriptor), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if err != nil {
		return false, err
	}
	if peerError != nil {
		return false, peerError
	}
	if peer.Pid != int32(pid) {
		return false, fmt.Errorf("control socket peer PID %d differs from target %d", peer.Pid, pid)
	}
	if err = connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return false, err
	}
	if _, err = io.WriteString(connection, request); err != nil {
		return false, err
	}
	if err = connection.(*net.UnixConn).CloseWrite(); err != nil {
		return false, err
	}
	reply, err := io.ReadAll(io.LimitReader(connection, 4097))
	text := string(reply)
	return len(reply) <= 4096 && (text == "ok" || text == "ok\n" || strings.HasPrefix(text, "ok ")), err
}

func openRecord(path, command string) (*os.File, error) {
	flags := syscall.O_RDWR | syscall.O_CLOEXEC | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	if command == "report" {
		flags = syscall.O_RDONLY | syscall.O_CLOEXEC | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	}
	var file *os.File
	var err error
	if command == "setup" {
		file, err = os.OpenFile(path, flags|syscall.O_CREAT|syscall.O_EXCL, 0640)
	}
	if command != "setup" || os.IsExist(err) {
		file, err = os.OpenFile(path, flags, 0)
	}
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil {
		status := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || status.Nlink != 1 || info.Mode().Perm()&0022 != 0 ||
			((command != "report" || os.Geteuid() == 0) && status.Uid != 0 && status.Uid != uint32(os.Geteuid())) {
			err = fmt.Errorf("unsafe coordination record owner, permissions, type or link count")
		}
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func run(arguments []string, environment ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, arguments[0], arguments[1:]...)
	command.WaitDelay = time.Second
	command.Env = append(os.Environ(), environment...)
	command.Stderr = os.Stderr
	return command.Output()
}

func buildID(path string) (string, error) {
	image, err := elf.Open(path)
	if err != nil {
		return "", err
	}
	defer image.Close()
	identity := ""
	for _, section := range image.Sections {
		if section.Type != elf.SHT_NOTE {
			continue
		}
		data, err := section.Data()
		if err != nil {
			return "", err
		}
		for len(data) >= 12 {
			names := uint64(image.ByteOrder.Uint32(data))
			size := uint64(image.ByteOrder.Uint32(data[4:]))
			kind := image.ByteOrder.Uint32(data[8:])
			description := uint64(12) + ((names + 3) &^ uint64(3))
			next := description + ((size + 3) &^ uint64(3))
			if next > uint64(len(data)) {
				break
			}
			if names == 4 && kind == 3 && size > 0 && size <= 64 && string(data[12:16]) == "GNU\x00" {
				identity = hex.EncodeToString(data[description : description+size])
			}
			data = data[next:]
		}
	}
	if identity == "" {
		return "", fmt.Errorf("GNU build-id missing")
	}
	return identity, nil
}

func mapFields(line string) ([5]string, string) {
	var fields [5]string
	for idx := range fields {
		line = strings.TrimLeft(line, " \t")
		end := strings.IndexAny(line, " \t")
		if end < 0 {
			return fields, ""
		}
		fields[idx], line = line[:end], line[end:]
	}
	return fields, strings.TrimLeft(line, " \t")
}

func translate(image *elf.File, value uint64, fromFile bool) (uint64, error) {
	for _, segment := range image.Progs {
		base, target := segment.Vaddr, segment.Off
		if fromFile {
			base, target = target, base
		}
		if segment.Type == elf.PT_LOAD && value >= base && value-base < segment.Filesz {
			return value - base + target, nil
		}
	}
	return 0, fmt.Errorf("address outside loadable image")
}

func sourceLine(path string, address uint64, cache map[string]*dwarf.Data) (string, error) {
	data := cache[path]
	if data == nil {
		image, err := elf.Open(path)
		if err != nil {
			return "", err
		}
		data, err = image.DWARF()
		image.Close()
		if err != nil {
			return "", err
		}
		cache[path] = data
	}

	unit, err := data.Reader().SeekPC(address)
	if err != nil {
		return "", err
	}
	reader, err := data.LineReader(unit)
	if err != nil {
		return "", err
	}
	if reader == nil {
		return "", fmt.Errorf("compilation unit has no line table")
	}

	var line dwarf.LineEntry
	if err := reader.SeekPC(address, &line); err != nil {
		return "", err
	}
	if line.File == nil || line.Line == 0 {
		return "", fmt.Errorf("address has no source line")
	}
	return fmt.Sprintf("%s:%d", line.File.Name, line.Line), nil
}

func lockOffsets(binary, debug string) ([3]uint64, error) {
	var offsets [3]uint64
	image, err := elf.Open(debug)
	if err != nil {
		return offsets, err
	}
	symbols, err := image.Symbols()
	image.Close()
	if err != nil {
		return offsets, err
	}
	executable, err := elf.Open(binary)
	if err != nil {
		return offsets, err
	}
	defer executable.Close()
	for idx, name := range []string{"cp_config_lock", "cp_config_try_lock", "cp_config_unlock"} {
		occurrences := 0
		for _, symbol := range symbols {
			if symbol.Name != name || elf.ST_TYPE(symbol.Info) != elf.STT_FUNC || symbol.Section == elf.SHN_UNDEF {
				continue
			}
			occurrences++
			offsets[idx], err = translate(executable, symbol.Value, false)
			if err != nil {
				return offsets, err
			}
		}
		if occurrences != 1 {
			return offsets, fmt.Errorf("missing/ambiguous lock symbol: %s", name)
		}
	}
	return offsets, nil
}

func runtimeEnvironment() {
	checkRuntimeFlags(os.Environ())
	_, present := os.LookupEnv("LD_LIBRARY_PATH")
	check(!present, "unqualified runtime environment: LD_LIBRARY_PATH")
}

func checkRuntimeFlags(entries []string) {
	for _, entry := range entries {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "BPFTIME_RUN_WITH_KERNEL", "BPFTIME_DISABLE_JIT", "BPFTIME_VM_NAME", "BPFTIME_HELPER_GROUPS", "BPFTIME_NOT_LOAD_PATTERN", "BPFTIME_ALLOW_EXTERNAL_MAPS":
			check(false, "unqualified runtime environment: "+name)
		}
	}
}
