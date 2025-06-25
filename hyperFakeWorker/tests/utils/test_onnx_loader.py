from pathlib import Path

from worker.utils.onnx import ONNXModelInferer

test_path = Path(__file__).parent

def test_onnx_loader():
    import numpy as np

    inferer = ONNXModelInferer(test_path.joinpath("test_model.onnx"))
    
    input_data = {
        "a": np.array([[1.0]], dtype=np.float32), 
        "b": np.array([[2.0]], dtype=np.float32)
        }
    
    output = inferer.infer(input_data)

    assert(output["sum"] == 3)
    
    print(f"Model output: {output}")
