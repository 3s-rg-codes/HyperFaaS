# hyperFake Model

## Training Data (last updated: 06.06.2025)

### Input

- Request Body size
- The number of function instances (number of containers that could potentially execute client code)
- The number of active function calls (number of containers in which user code is actively executing a call)
- Worker resource consumption

**Worker resource consumption** is the sum of the resource consumption of all running functions.

### Output

- Function runtime
- Function resource consumption (average throughout execution)

## Training Data Format

Each row should contain the information for one function call.

| Parameter | Body Size | Number of Function Instances | Number of active Function Calls | Worker CPU Usage | Worker RAM Usage | Function Runtime | Function CPU Usage | Function RAM Usage |
| --------- | --------- | ---------------------------- | ------------------------------- | ---------------- | ---------------- | ---------------- | ------------------ | ------------------ |
| Unit      | Byte      | Count                        | Count                           | Cores            | Bytes            | Nanoseconds      | Cores              | Bytes              |
| Type      | Int64     | Int64                        | Int64                           | Float64          | Int64            | Int64            | Float64            | Int64              |
