"""One isolated E2E case. The parent owns timeouts, process cleanup and aggregation."""
import importlib.util
import json
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))


def main():
    case_path, context_path, result_path = map(Path, sys.argv[1:])
    context = json.loads(context_path.read_text())
    for key in ("bin_path", "case_dir", "runner_py", "root_dir", "policy_path"):
        if key in context:
            context[key] = Path(context[key])
    spec = importlib.util.spec_from_file_location(case_path.stem, case_path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[case_path.stem] = module
    spec.loader.exec_module(module)
    result = module.run(context)
    result_path.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")


if __name__ == "__main__":
    main()
