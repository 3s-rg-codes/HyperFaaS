import sys
from pathlib import Path

def add_to_python_path(new_path: Path):
    existing_path = sys.path
    absolute_path = new_path.absolute()
    if absolute_path not in existing_path:
        sys.path.append(absolute_path.as_posix())
    return sys.path

def add_proto_definitions():
    current_path = Path(__file__).parent
    add_to_python_path(
        current_path
    )