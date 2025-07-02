import sqlite3
import pandas as pd
import json

class Data:
    def __init__(self):
        self.metrics = None
        self.cold_starts = None
        self.scenarios = None

    def load_metrics(self, db_path: str) -> pd.DataFrame:
        """Get request latency for each function from database."""
        
        conn = sqlite3.connect(db_path)
        
        query = """
        SELECT
            *
        FROM metrics
        """
        
        df = pd.read_sql_query(query, conn)
        conn.close()
        
        # Preprocess timestamps
        df['callqueuedtimestamp'] = pd.to_datetime(df['callqueuedtimestamp'], unit='ns')
        df['leafgotrequesttimestamp'] = pd.to_datetime(df['leafgotrequesttimestamp'], unit='ns')
        df['leafscheduledcalltimestamp'] = pd.to_datetime(df['leafscheduledcalltimestamp'], unit='ns')
        df['gotresponsetimestamp'] = pd.to_datetime(df['gotresponsetimestamp'], unit='ns')
        
        # Manual workaround for pandas datetime conversion bug with NaN values
        # https://github.com/pandas-dev/pandas/pull/61022
        # https://github.com/pandas-dev/pandas/issues/58419
        # Convert timeout column
        timeout_series = df['timeout']
        timeout_valid_mask = timeout_series.notna()
        timeout_result = pd.Series(index=timeout_series.index, dtype='datetime64[ns]')
        if timeout_valid_mask.any():
            timeout_result.loc[timeout_valid_mask] = pd.to_datetime(timeout_series[timeout_valid_mask], unit='ms')
        df['timeout'] = timeout_result
        
        # Convert error column  
        error_series = df['error']
        error_valid_mask = error_series.notna()
        error_result = pd.Series(index=error_series.index, dtype='datetime64[ns]')
        if error_valid_mask.any():
            error_result.loc[error_valid_mask] = pd.to_datetime(error_series[error_valid_mask], unit='ms')
        df['error'] = error_result
        
        df['timestamp'] = pd.to_datetime(df['timestamp'], unit='s')
        
        # Add computed latency columns
        df['scheduling_latency_ms'] = (df['leafscheduledcalltimestamp'] - df['leafgotrequesttimestamp']).dt.total_seconds() * 1000
        df['leaf_to_worker_latency_ms'] = (df['callqueuedtimestamp'] - df['leafscheduledcalltimestamp']).dt.total_seconds() * 1000
        df['function_processing_latency_ms'] = (df['gotresponsetimestamp'] - df['callqueuedtimestamp']).dt.total_seconds() * 1000
        
        self.metrics = df
        return df

    def load_cold_start_times(self, db_path: str) -> pd.DataFrame:
        """Calculate cold start times for each function instance."""
            
        conn = sqlite3.connect(db_path)

        query = """
        WITH start_events AS (
            SELECT 
                instance_id,
                function_id,
                timestamp as start_time
            FROM status_updates
            WHERE event = 3 AND status = 0  -- EVENT_START, STATUS_SUCCESS
        ),
        running_events AS (
            SELECT 
                instance_id,
                function_id,
                timestamp as running_time
            FROM status_updates
            WHERE event = 6 AND status = 0  -- EVENT_RUNNING, STATUS_SUCCESS
        )
        SELECT 
            s.function_id as function_id,
            s.instance_id as instance_id,
            fi.image_tag as image_tag,
            s.start_time as start_time,
            r.running_time as running_time,
            (julianday(r.running_time) - julianday(s.start_time)) * 24 * 60 * 60 * 1000 as cold_start_ms
        FROM start_events s
        JOIN running_events r ON s.instance_id = r.instance_id
        JOIN function_images fi ON s.function_id = fi.function_id
        ORDER BY s.function_id, s.instance_id
        """

        df = pd.read_sql_query(query, conn)
        conn.close()
        
        self.cold_starts = df
        return df

    def load_k6_scenarios(self, scenarios_path: str) -> pd.DataFrame:
        """Parse k6 scenarios JSON and create a timeline dataframe of expected RPS."""
        
        with open(scenarios_path, 'r') as f:
            data = json.load(f)
        
        scenarios = data['scenarios']
        
        total_duration_str = data['metadata']['totalDuration']
        if total_duration_str.endswith('m'):
            total_duration_seconds = int(total_duration_str.rstrip('m')) * 60
        elif total_duration_str.endswith('s'):
            total_duration_seconds = int(total_duration_str.rstrip('s'))
        else:
            raise ValueError(f"Invalid total duration: {total_duration_str}")
        
        timeline_data = []
        
        # Calculate the expected rps for each second
        for second in range(total_duration_seconds):
            rps_by_image = {}
            
            for scenario_name, scenario in scenarios.items():
                image_tag = scenario['tags']['image_tag']
                
                start_time_str = scenario['startTime']
                start_time = int(start_time_str.rstrip('s'))
                
                if scenario['executor'] == 'constant-arrival-rate':
                    duration = int(scenario['duration'].rstrip('s'))
                    end_time = start_time + duration
                    
                    # Check if this second falls within the scenario duration
                    if start_time <= second < end_time:
                        rate = scenario['rate']
                        if image_tag not in rps_by_image:
                            rps_by_image[image_tag] = 0
                        rps_by_image[image_tag] += rate
                        
                elif scenario['executor'] == 'ramping-arrival-rate':
                    # Calculate the rate for this specific second
                    start_rate = scenario['startRate']
                    stage = scenario['stages'][0]  # Important: we assume a single stage !
                    target_rate = stage['target']
                    stage_duration = int(stage['duration'].rstrip('s'))
                    end_time = start_time + stage_duration
                    
                    # Check if this second falls within the scenario duration
                    if start_time <= second < end_time:
                        progress = (second - start_time) / stage_duration
                        current_rate = start_rate + (target_rate - start_rate) * progress
                        
                        if image_tag not in rps_by_image:
                            rps_by_image[image_tag] = 0
                        rps_by_image[image_tag] += current_rate
            
            # Add entries for each image tag at this second
            for image_tag, rps in rps_by_image.items():
                timeline_data.append({
                    'second': second,
                    'image_tag': image_tag,
                    'expected_rps': rps
                })
            
            # If no scenarios are active for any image at this second, add zero entries
            if not rps_by_image:
                # Get all unique image tags from scenarios
                all_image_tags = set(scenario['tags']['image_tag'] for scenario in scenarios.values())
                for image_tag in all_image_tags:
                    timeline_data.append({
                        'second': second,
                        'image_tag': image_tag,
                        'expected_rps': 0
                    })
        
        df = pd.DataFrame(timeline_data)
        
        # Fill missing combinations with 0
        if not df.empty:
            all_seconds = range(total_duration_seconds)
            all_image_tags = df['image_tag'].unique()
            
            # Create complete index
            complete_index = pd.MultiIndex.from_product([all_seconds, all_image_tags], 
                                                    names=['second', 'image_tag'])
            complete_df = pd.DataFrame(index=complete_index).reset_index()
            
            # Merge with actual data
            df = complete_df.merge(df, on=['second', 'image_tag'], how='left')
            df['expected_rps'] = df['expected_rps'].fillna(0)
        
        self.scenarios = df
        return df