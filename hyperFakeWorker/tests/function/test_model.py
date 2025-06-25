from pathlib import Path
import numpy as np

from worker.function.model import FunctionModelInferer, FunctionModelInput, FunctionModelOutput

test_path = Path(__file__).parent

bfs_model = test_path.joinpath("bfs.onnx")
echo_model = test_path.joinpath("echo.onnx")
thumbnailer_model = test_path.joinpath("thumbnailer.onnx")

def test_FunctionModelInferer():
    inferer = FunctionModelInferer(bfs_model)
    assert inferer.model is not None, "Failed to load ONNX model"

    input_data = FunctionModelInput(100, 5, 5, 5.5, 1024)
    input_tensor = input_data.as_tensor()
    assert input_tensor.shape == (1, 5), "Input tensor has invalid shape"
    assert input_tensor.dtype == np.float32, "Input tensor has invalid dtype"

    output_data = inferer.infer(input_data)