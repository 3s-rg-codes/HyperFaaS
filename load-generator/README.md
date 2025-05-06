# k6 Load Generator Configuration Guide

Thanks to ChatGPT for helping me with this README.

## Disclaimer

Right now, this is a very early version. When I tested it, it worked, but it might not for you.
Also, the code structure is really not that good yet, and I've made some questionable decisions, but I will improve that in the near future.
~ Bennet, 06.05.2025

## JSON Configuration for Function Parameters
The JSON configuration in `config/config.json` defines the function that will be executed in the k6 script, its associated service, and the parameters that will be passed to it. Here's the base format:

```json
{
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
2,40,20,20,10
3,60,30,30,10
```

## Run

To run your configured test, use `just`.
