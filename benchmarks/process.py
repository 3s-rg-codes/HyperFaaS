import sqlite3
import pandas as pd
from datetime import datetime, timedelta
import numpy as np
from typing import Dict, List, Tuple
import argparse
from tqdm import tqdm

# METRICS TABLE
# callqueuedtimestamp and gotresponsetimestamp are in nanoseconds
# sqlite> select leafgotrequesttimestamp, leafscheduledcalltimestamp,callqueuedtimestamp,gotresponsetimestamp from metrics limit 1;
# 1.74953924495481e+18|1.7495392457413e+18|1.74953924574153e+18|1.74953924574764e+18

# CPU_MEM_STATS TABLE
# timestamp is in seconds
# sqlite> select timestamp from cpu_mem_stats limit 1;
# 1749539243

class TrainingData:
    def __init__(self, db_path: str, active_calls_window_size: int):
        """Initialize the database processor with the path to the SQLite database."""
        self.db_path = db_path
        self.conn = None
        self.active_calls_window_size = active_calls_window_size
        
    def connect(self):
        """Connect to the SQLite database."""
        try:
            self.conn = sqlite3.connect(self.db_path)
            print(f"Connected to database: {self.db_path}")
        except sqlite3.Error as e:
            print(f"Error connecting to database: {e}")
            raise
    
    def close(self):
        """Close the database connection."""
        if self.conn:
            self.conn.close()
            print("Database connection closed")
    
    def create_training_data_table(self):
        """Create the training_data table with the specified schema."""
        create_table_sql = """
        CREATE TABLE IF NOT EXISTS training_data (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            request_id TEXT,
            function_image_tag TEXT,
            timestamp INTEGER,
            request_body_size INTEGER,
            function_instances_count INTEGER,
            active_function_calls_count INTEGER,
            worker_cpu_usage REAL,
            worker_ram_usage INTEGER,
            function_runtime INTEGER,
            function_cpu_usage REAL,
            function_ram_usage INTEGER
        );
        """
        
        try:
            cursor = self.conn.cursor()
            cursor.execute(create_table_sql)
            self.conn.commit()
            print("Training data table created successfully")
        except sqlite3.Error as e:
            print(f"Error creating training_data table: {e}")
            raise
    
    def get_metrics_data(self) -> pd.DataFrame:
        """Fetch all metrics data with optimized data types."""
        query = """
        SELECT 
            request_id,
            instance_id,
            image_tag,
            grpc_req_duration,
            callqueuedtimestamp,
            gotresponsetimestamp,
            leafgotrequesttimestamp,
            leafscheduledcalltimestamp,
            functionprocessingtime,
            data_sent,
            data_received
        FROM metrics 
        WHERE request_id IS NOT NULL
        ORDER BY callqueuedtimestamp
        """
        
        try:
            df = pd.read_sql_query(query, self.conn)
            
            # convert to appropriate data types
            df['callqueuedtimestamp'] = pd.to_numeric(df['callqueuedtimestamp'], downcast='float')
            df['gotresponsetimestamp'] = pd.to_numeric(df['gotresponsetimestamp'], downcast='float')
            df['leafgotrequesttimestamp'] = pd.to_numeric(df['leafgotrequesttimestamp'], downcast='float')
            df['leafscheduledcalltimestamp'] = pd.to_numeric(df['leafscheduledcalltimestamp'], downcast='float')
            
            df['functionprocessingtime'] = pd.to_numeric(df['functionprocessingtime'], downcast='float')
            df['data_sent'] = pd.to_numeric(df['data_sent'], downcast='integer', errors='coerce')
            df['data_received'] = pd.to_numeric(df['data_received'], downcast='integer', errors='coerce')
            
            print(f"Fetched {len(df)} metrics records")
            return df
        except Exception as e:
            print(f"Error fetching metrics data: {e}")
            raise
    
    def get_cpu_mem_stats(self) -> pd.DataFrame:
        """Fetch CPU and memory statistics with optimized data types."""
        query = """
        SELECT 
            instance_id,
            function_id,
            image_tag,
            timestamp,
            cpu_usage_percent,
            memory_usage,
            memory_usage_limit,
            memory_usage_percent
        FROM cpu_mem_stats
        ORDER BY timestamp
        """
        
        try:
            df = pd.read_sql_query(query, self.conn)
            
            # everything to nanoseconds
            df['timestamp'] = pd.to_numeric(df['timestamp'], downcast='float') * 1e9
            
            df['cpu_usage_percent'] = pd.to_numeric(df['cpu_usage_percent'], downcast='float')
            df['memory_usage'] = pd.to_numeric(df['memory_usage'], downcast='integer', errors='coerce')
            df['memory_usage_limit'] = pd.to_numeric(df['memory_usage_limit'], downcast='integer', errors='coerce')
            df['memory_usage_percent'] = pd.to_numeric(df['memory_usage_percent'], downcast='float')
            
            print(f"Fetched {len(df)} CPU/memory stats records")
            return df
        except Exception as e:
            print(f"Error fetching CPU/memory stats: {e}")
            raise
    
    def precompute_active_function_calls(self, metrics_df: pd.DataFrame) -> Dict[float, int]:
        """
        Pre-calculate the number of active function calls for all timestamps.
        This is much more efficient than calculating for each timestamp individually (that was taking hours).
        """
        print("Pre-computing active function calls...")

        # What do we define as active function call?
        start_key = 'callqueuedtimestamp'
        #end_key = 'gotresponsetimestamp'
        #start_key = 'leafgotrequesttimestamp'
        end_key = 'gotresponsetimestamp'

        # window size in milliseconds
        window_size = self.active_calls_window_size
        
        # Get all unique timestamps
        unique_timestamps = sorted(metrics_df['callqueuedtimestamp'].unique())
        active_calls_dict = {}
        
        # Convert to numpy arrays for faster operations
        start_timestamps = metrics_df[start_key].values
        end_timestamps = metrics_df[end_key].values
        
        for timestamp in tqdm(unique_timestamps, desc="Computing active calls"):
            active_mask = (start_timestamps - window_size <= timestamp) & (timestamp <= end_timestamps + window_size)
            active_calls_dict[timestamp] = int(np.sum(active_mask))
        
        print(f"Pre-computed active calls for {len(unique_timestamps)} unique timestamps")
        return active_calls_dict
    


    def precompute_worker_stats_by_second(self, stats_df: pd.DataFrame) -> Dict[int, Dict]:
        """
        Pre-compute worker stats aggregated by second (timestamp).
        """
        print("Pre-computing worker stats by second...")
        
        # Convert timestamp from nanoseconds back to seconds for grouping
        stats_df_copy = stats_df.copy()
        stats_df_copy['timestamp_seconds'] = (stats_df_copy['timestamp'] / 1e9).astype(int)
        
        # Group by second and compute aggregated stats
        worker_stats = {}
        for timestamp_sec, group in tqdm(stats_df_copy.groupby('timestamp_seconds'), desc="Computing worker stats"):
            unique_instances = group['instance_id'].nunique()
            avg_cpu_usage = group['cpu_usage_percent'].mean()
            avg_memory_usage = group['memory_usage'].mean()
            
            worker_stats[timestamp_sec] = {
                'function_instances_count': int(unique_instances),
                'worker_cpu_usage': float(avg_cpu_usage),
                'worker_ram_usage': int(avg_memory_usage) if not pd.isna(avg_memory_usage) else 0
            }
        
        print(f"Pre-computed worker stats for {len(worker_stats)} seconds")
        return worker_stats
    
    def precompute_instance_stats_by_second(self, stats_df: pd.DataFrame) -> Dict[Tuple[str, int], Tuple[float, int]]:
        """
        Pre-compute instance stats by second and instance_id
        (instance_id, timestamp_seconds) -> (cpu_usage, ram_usage)
        """
        print("Pre-computing instance stats by second...")
        
        # Convert timestamp from nanoseconds back to seconds
        stats_df_copy = stats_df.copy()
        stats_df_copy['timestamp_seconds'] = (stats_df_copy['timestamp'] / 1e9).astype(int)
        
        instance_stats = {}
        for _, row in tqdm(stats_df_copy.iterrows(), total=len(stats_df_copy), desc="Computing instance stats"):
            instance_id = row['instance_id']
            timestamp_sec = row['timestamp_seconds']
            cpu_usage = float(row['cpu_usage_percent'])
            ram_usage = int(row['memory_usage']) if pd.notna(row['memory_usage']) else 0
            
            instance_stats[(instance_id, timestamp_sec)] = (cpu_usage, ram_usage)
        
        print(f"Pre-computed instance stats for {len(instance_stats)} combinations")
        return instance_stats

    def process_training_data(self):
        """Main method to process and create training data."""
        print("Starting training data processing...")
    
        metrics_df = self.get_metrics_data()
        stats_df = self.get_cpu_mem_stats()
        
        if len(metrics_df) == 0:
            print("No metrics data found")
            return
        
        active_calls_lookup = self.precompute_active_function_calls(metrics_df)
        
        worker_stats_lookup = self.precompute_worker_stats_by_second(stats_df)
        instance_cpu_ram_lookup = self.precompute_instance_stats_by_second(stats_df)
        
        training_data = []
        total_requests = len(metrics_df)
        
        for _, row in tqdm(metrics_df.iterrows(), total=total_requests, desc="Processing requests"):
            # we insert 0 values for all metrics that are not available for now. I think this fks up the training so we should try to find a better way to handle this.
            try:
                request_id = row['request_id']
                timestamp = row['callqueuedtimestamp']
                function_image_tag = row['image_tag']
                instance_id = row['instance_id']
                
                # Calculate request body size
                request_body_size = int(row['data_sent']) if pd.notna(row['data_sent']) else 0
                active_calls = active_calls_lookup.get(timestamp, 0)
                
                worker_stats = worker_stats_lookup.get(int(timestamp / 1e9), {
                    'function_instances_count': 0,
                    'worker_cpu_usage': 0.0,
                    'worker_ram_usage': 0
                })
                
                function_cpu_usage, function_ram_usage = instance_cpu_ram_lookup.get(
                    (instance_id, int(timestamp / 1e9)), (0.0, 0)
                )
                
                # Function runtime (convert to nanoseconds)
                function_runtime = int(row['functionprocessingtime'] * 1e9) if pd.notna(row['functionprocessingtime']) else 0
                
                training_record = {
                    'request_id': request_id,
                    'timestamp': int(timestamp),
                    'request_body_size': request_body_size,
                    'function_image_tag': function_image_tag,
                    'function_instances_count': int(worker_stats['function_instances_count']),
                    'active_function_calls_count': int(active_calls),
                    'worker_cpu_usage': float(worker_stats['worker_cpu_usage']),
                    'worker_ram_usage': int(worker_stats['worker_ram_usage']),
                    'function_runtime': int(function_runtime),
                    'function_cpu_usage': float(function_cpu_usage),
                    'function_ram_usage': int(function_ram_usage)
                }
                
                training_data.append(training_record)
                
            except Exception as e:
                print(f"Error processing record {row.get('request_id', 'unknown')}: {e}")
                continue
        
        print(f"Processed {len(training_data)} training records")
        return training_data
    
    def insert_training_data(self, training_data: List[Dict]):
        """Insert the processed training data into the training_data table."""
        if not training_data:
            print("No training data to insert")
            return
        
        insert_sql = """
        INSERT INTO training_data (
            request_id, timestamp, request_body_size, function_image_tag, function_instances_count,
            active_function_calls_count, worker_cpu_usage, worker_ram_usage,
            function_runtime, function_cpu_usage, function_ram_usage
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        """
        
        try:
            cursor = self.conn.cursor()
            
            # Clear existing data
            cursor.execute("DELETE FROM training_data")
            
            # Insert new data
            for record in training_data:
                cursor.execute(insert_sql, (
                    record['request_id'],
                    record['timestamp'],
                    record['request_body_size'],
                    record['function_image_tag'],
                    record['function_instances_count'],
                    record['active_function_calls_count'],
                    record['worker_cpu_usage'],
                    record['worker_ram_usage'],
                    record['function_runtime'],
                    record['function_cpu_usage'],
                    record['function_ram_usage']
                ))
            
            self.conn.commit()
            print(f"Successfully inserted {len(training_data)} training records")
            
        except sqlite3.Error as e:
            print(f"Error inserting training data: {e}")
            self.conn.rollback()
            raise
    
    def run(self):
        """Main execution method."""
        try:
            self.connect()
            self.create_training_data_table()
            training_data = self.process_training_data()
            if training_data:
                self.insert_training_data(training_data)
                print("Training data processing completed successfully")
            else:
                print("No training data was generated")
        except Exception as e:
            print(f"Error during processing: {e}")
            raise
        finally:
            self.close()

def main():
    """Main function to run the database processor."""
    parser = argparse.ArgumentParser(description='Process metrics database')
    parser.add_argument('--db-path', default='metrics.db', help='Path to SQLite database')
    parser.add_argument('--active-calls-window-size', type=int, default=100, help='Window size in milliseconds for active calls')
    args = parser.parse_args()
    try:
        processor = TrainingData(args.db_path, args.active_calls_window_size/2)
        processor.run()
        print("Processing completed successfully!")
    except Exception as e:
        import traceback
        traceback.print_exc()
        print(f"Error: {e}")

if __name__ == "__main__":
    main()
