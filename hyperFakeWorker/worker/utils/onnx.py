from pathlib import Path
from typing import Any

import onnxruntime as ort

class ONNXModelInferer():
    def __init__(self, model_path: Path):
        """
        Load an ONNX model from the specified path.
        Args:
            model_path (Path): The path to the ONNX model file.
        """
        try:
            self.model = ort.InferenceSession(model_path)
        except Exception as e:
            raise RuntimeError(f"Failed to load ONNX model from {model_path}: {e}")
        
    def infer(self, input_data: dict[str, Any]) -> dict:
        """
        Perform inference using the loaded ONNX model.
        Args:
            input_data (dict): A dictionary containing input data for the model.
        Returns:
            dict: The output of the model inference.
        """
        try:
            outputs = self.model.run(None, input_data)
            output_names = [output.name for output in self.model.get_outputs()]
            return {name: output for name, output in zip(output_names, outputs)}
        except Exception as e:
            raise RuntimeError(f"Failed to perform inference: {e}")