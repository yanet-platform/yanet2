#!/usr/bin/env python3
import argparse
import os
import pathlib
import sys
import subprocess
from multiprocessing import shared_memory

def main():
    if len(sys.argv) < 3:
        print(f"Usage: {sys.argv[0]} <COMPILER_EXE_PATH> <FILTER_EXE_PATH>")
        sys.exit(1)
    
    compiler_path = sys.argv[1]
    filter_path = sys.argv[2]
    
    print("Compiler path: ", compiler_path)
    print("Filter path: ", filter_path)
    
    size = 1<<24
    
    shm = shared_memory.SharedMemory(create=True, size=size)

    rc1 = rc2 = 0
    try:
        print(f"Allocated shared memory: name={shm.name} size={size}")
        print(f"Running compiler: {compiler_path}")
        p1 = subprocess.run([compiler_path, shm.name, str(size)], check=False)
        rc1 = p1.returncode
        print(f"Compiler exited with code {rc1}")
        if rc1 != 0:
            print(f"Failed to compile filter")
            raise Exception("Failed to compile filter")

        print(f"Running filter: {filter_path}")
        p2 = subprocess.run([filter_path, shm.name, str(size)], check=False)
        rc2 = p2.returncode
        print(f"Filter exited with code {rc2}")
        if rc2 != 0:
            print(f"Failed to filter packets")
            raise Exception("Failed to filter packets")
    except Exception:
        shm.unlink()
        exit(1)
    shm.unlink()
    
    print('OK')
    

if __name__ == "__main__":
    main()