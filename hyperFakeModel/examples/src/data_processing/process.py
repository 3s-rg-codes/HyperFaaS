#!/bin/python
from pathlib import Path
import click
import sqlite3 as sql

import polars as pl
import tqdm

from utilities.log import logger

class SQLiteWrapper():

    def __init__(self, database_file: Path):
        self.database_file = database_file


    def resources_as_df(self) -> pl.DataFrame:
        query = """
                SELECT
                *
                FROM cpu_mem_stats
                """
        with sql.connect(self.database_file) as conn:
            df = pl.read_database(query=query, connection=conn, infer_schema_length=None)
        df = df.filter(pl.col("timestamp") != "0001-01-01 00:00:00+00:00")
        df = df.with_columns(
            pl.col("timestamp").str.to_datetime().dt.truncate(every="1s")
        )
        df = df.group_by(["timestamp", "instance_id", "function_id"]).last()
        df = df.sort(["instance_id", "function_id", "timestamp"])
        df = df.with_columns(
            pl.col("cpu_total_usage").diff().over(["instance_id", "function_id"]).fill_null(pl.col("cpu_total_usage")).alias("cpu_usage")
        )
        df = df.sort("timestamp")
        df = df.group_by("timestamp").agg(
            pl.col("cpu_usage").sum(),
            pl.col("memory_usage").sum()
        )
        df = df.with_columns(
            pl.col("timestamp").dt.epoch(time_unit="s")
        )
        return df
    
    def metrics_as_df(self) -> pl.DataFrame:
        query = """
                SELECT
                *
                FROM metrics
                """
        with sql.connect(self.database_file) as conn:
            df = pl.read_database(query=query, connection=conn, infer_schema_length=None)
        return df


@click.command()
@click.argument("database", metavar="[Databasefile]", type=click.Path(path_type=Path, exists=True, dir_okay=False, readable=True, resolve_path=True))
@click.argument("resources", metavar="[Resource Usage Data]", type=click.Path(path_type=Path, exists=False, dir_okay=False, resolve_path=True))
@click.argument("metrics", metavar="[Metrics Data]", type=click.Path(path_type=Path, exists=False, dir_okay=False, resolve_path=True))
@click.argument("training", metavar="[Training Data]", type=click.Path(path_type=Path, exists=False, dir_okay=False, resolve_path=True))
def main(database: Path, resources: Path, metrics: Path, training: Path):
    logger.info(f"Preprocessing Data from {database}")

    db = SQLiteWrapper(database)
    
    resourcesDf = db.resources_as_df()
    resourcesDf.write_csv(resources)

    df = db.metrics_as_df()
    df = df.filter(
        (pl.col("callqueuedtimestamp").is_not_nan() & pl.col("callqueuedtimestamp").is_not_null()),
        (pl.col("gotresponsetimestamp").is_not_nan() & pl.col("gotresponsetimestamp").is_not_null()),
    )
    df.write_csv(metrics)

    functions = df.select(pl.col("image_tag").unique()).to_series().to_list()

    start_events = df.select(
        pl.col("image_tag"),
        pl.col("callqueuedtimestamp").alias("timestamp")
    ).with_columns(pl.lit(1).alias("delta"))

    end_events = df.select(
        pl.col("image_tag"),
        (pl.col("gotresponsetimestamp") + 1).alias("timestamp")
    ).with_columns(pl.lit(-1).alias("delta"))

    # Combine start and end events
    events: pl.DataFrame = pl.concat([start_events, end_events])

    events = events.with_columns(
        pl.col("timestamp").dt.epoch(time_unit="s")
    )
    print(events.schema)

    # Group by timestamp, sum deltas (to merge multiple events at same time)
    events = events.group_by(["timestamp", "image_tag"]).agg(pl.col('delta').sum())

    # Sort by timestamp
    events = events.sort(["timestamp"])

    events = events.pivot(on="image_tag", index="timestamp", values="delta").fill_null(0)

    # Compute cumulative sum to get active calls at each timestamp
    for function in functions:
        events = events.with_columns(
            pl.col(function).cum_sum().alias(f"{function}_active_calls")
        )

    events.write_csv("jamoin.csv")

    trainDf = resourcesDf.join(other=events, on="timestamp")
    trainDf = trainDf.with_columns(
        pl.col("cpu_usage").truediv(1000000000),
        pl.col("memory_usage").truediv(1000000)
    )
    trainDf.write_csv(training)

if __name__ == "__main__":
    main()
