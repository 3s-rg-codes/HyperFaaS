from dataclasses import dataclass

import numpy as np
from ..utils.onnx import ONNXModelInferer

@dataclass
class FunctionModelInput():
    body_size: int
    function_instances: int
    function_calls: int
    cpu_usage: float
    ram_usage: int

    def as_tensor(self) -> np.ndarray:
        return np.array(
            [[
                self.body_size,
                self.function_instances,
                self.function_calls,
                self.cpu_usage,
                self.ram_usage
            ]],
            dtype=np.float32
        )

@dataclass
class FunctionModelOutput():
    function_runtime: int
    cpu_usage: float
    ram_usage: int

    @staticmethod
    def from_tensor(tensor: np.ndarray) -> "FunctionModelOutput":
        values = tensor.flatten()

        if values.size != 3:
            raise ValueError(f"Expected 3 otuput values, got {values.size}: {values}")

        return FunctionModelOutput(
            function_runtime=int(values[0]),
            cpu_usage=float(values[1]),
            ram_usage=int(values[2])
        )

class FunctionModelInferer(ONNXModelInferer):

    def infer(self, input_data: FunctionModelInput) -> FunctionModelOutput:
        """
        Perform inference using the loaded ONNX model.
        Args:
            input_data (FunctionModelInput): A FunctionModelInput object
        Returns:
            FunctionModelOutput: The output of the model inference.
        """
        tensor_input = input_data.as_tensor()

        input_names = self.model.get_inputs()

        if len(input_names) != 1:
            raise ValueError(f"Expected exactly one input in the model, got {len(input_names)}.")

        input_name = input_names[0].name

        outputs = self.model.run(None, {input_name: tensor_input})

        output_tensor = outputs[0]

        return FunctionModelOutput.from_tensor(output_tensor)