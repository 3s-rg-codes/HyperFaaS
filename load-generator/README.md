# k6 Load Generator Configuration Guide

Thanks to ChatGPT for helping me with this README.

## JSON Configuration for Function Parameters
The JSON configuration in `config/config.json` defines the function that will be executed in the k6 script, its associated service, the parameters that will be passed to it, and some metadata. 
Here's the base format:

```json
{
    "metaData": {
        "paramFile": "yourCSVFile.csv",
        "protoFile": "yourProtoFile.proto",
    },
    "function": {
        "name": "MyFunction",
        "serviceFn": "test.MyService/MyFunction",
        "params": [
            "param1",
            "param2",
            "..."
        ]
    }
}
```

## Defining Test Scenarios With Custom Values

The next part of the configuration involves defining the test scenarios in `config/config.csv`. This is where you set the values for `seconds`, `vus` (Virtual Users), `rps` (Requests Per Second), and your function parameters. Here's the format:
```csv
seconds,vus,rps,param1,param2,...
1,50,10,x,y
2,30,20,z,a
3,70,30,b,c
```

## Run

To run your configured test, use `just`.
