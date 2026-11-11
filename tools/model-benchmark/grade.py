"""Runs only inside the restricted grading container; never on the web host."""
import io
import json
import sys
from decimal import Decimal, InvalidOperation

sys.path.insert(0, "/lcb")
from lcb_runner.evaluation.compute_code_generation_metrics import check_correctness
import lcb_runner.evaluation.testing_util as testing_util


class AdvancingBuffer(io.BytesIO):
    def __init__(self, text):
        super().__init__(text.encode("utf-8"))


def main():
    payload = json.load(sys.stdin)
    testing_util.MockBuffer = AdvancingBuffer
    tolerance = payload.get("float_tolerance")
    if tolerance is not None:
        limit = Decimal(str(tolerance))

        class TolerantDecimal(Decimal):
            def __eq__(self, other):
                try:
                    left, right = Decimal(self), Decimal(other)
                    return left.is_finite() and right.is_finite() and abs(left - right) <= max(limit, limit * abs(right))
                except (InvalidOperation, TypeError, ValueError):
                    return False

        def convert_line(line):
            try:
                return True, [TolerantDecimal(word) for word in line.split()]
            except (InvalidOperation, ValueError):
                return False, []

        testing_util.convert_line_to_decimals = convert_line
    try:
        results, metadata = check_correctness(payload["sample"], payload["code"], timeout=6, debug=False)
        results = [value.item() if hasattr(value, "item") else value for value in results]
        expected = len(json.loads(payload["sample"]["input_output"])["inputs"])
        passed = len(results) == expected and all(type(value) in (bool, int) and value == 1 for value in results)
        result = {"passed": passed, "tests": results, "test_count": expected, "metadata": metadata}
    except Exception as exc:
        result = {"infrastructure_error": type(exc).__name__}
    print("BENCHMARK_RESULT=" + json.dumps(result, default=str))


if __name__ == "__main__":
    main()
