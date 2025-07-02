import csv
import sqlite3
from collections import defaultdict
import re
import json

# There is a bug somewhere inthe data pipeline right now. 
# sqlite> select count(distinct(request_id)) from metrics where grpc_req_duration is null and timeout is null and error is null;
# 214599
# This doesnt really make sense. it should be 0.
# It looks like its k6 . I revised the end of the csv file generated with a 10m big workload, and the last requests are not logging neither grpc_req_duration, nor timeout, nor error.
#  Idk if this is a VU issue or something else.
# Maybe it was my laptop that was lagging.

def create_tables(conn):
    cursor = conn.cursor()
    
    # one row per request is the idea
    cursor.execute('''
    CREATE TABLE IF NOT EXISTS metrics (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        timestamp INTEGER, -- timestamp of the logging of the request
        scenario TEXT,
        service TEXT,
        image_tag TEXT,
        instance_id TEXT,
        request_id TEXT,
        
        -- Request timing metrics
        grpc_req_duration REAL,
        callqueuedtimestamp REAL,
        gotresponsetimestamp REAL,
        functionprocessingtime REAL,
        leafgotrequesttimestamp REAL,
        leafscheduledcalltimestamp REAL,
        timeout REAL, -- timestamp of the timeout of the request
        error REAL, -- timestamp of the error of the request
        
        -- Data metrics
        data_sent REAL,
        data_received REAL,
        function_parameters TEXT,
        
        -- Other metadata
        proto TEXT,
        subproto TEXT,
        group_name TEXT,
        extra_tags TEXT,
        
        created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
    )
    ''')
    
    # Create function_images table
    cursor.execute('''
    CREATE TABLE IF NOT EXISTS function_images (
        function_id TEXT PRIMARY KEY,
        image_tag TEXT
    )
    ''')
    
    conn.commit()

def parse_tags(tags_str):
    """Parse key-value pairs from a string like 'key1=value1&key2=value2'"""
    if not tags_str:
        return {}
    
    result = {}
    # Handle cases where the tag might have equals signs in its value
    pairs = re.findall(r'([^&=]+)=([^&]*)', tags_str)
    for key, value in pairs:
        result[key] = value
    
    return result

def import_function_images(conn, json_file='generated_scenarios.json'):
    """Import function IDs and their image tags from the scenarios JSON file"""
    with open(json_file, 'r') as f:
        data = json.load(f)
    
    cursor = conn.cursor()
    
    # Handle both single run format and multiple runs format
    if 'runs' in data:
        # Multiple runs format - use the first run's metadata for function IDs
        first_run = data['runs'][0]
        function_images = [
            (first_run['metadata']['bfsFunctionId'], first_run['metadata']['configuration']['BFS_IMAGE_TAG']),
            (first_run['metadata']['echoFunctionId'], first_run['metadata']['configuration']['ECHO_IMAGE_TAG']),
            (first_run['metadata']['thumbnailerFunctionId'], first_run['metadata']['configuration']['THUMBNAILER_IMAGE_TAG'])
        ]
    else:
        # Single run format (backward compatibility)
        function_images = [
            (data['metadata']['bfsFunctionId'], data['metadata']['configuration']['BFS_IMAGE_TAG']),
            (data['metadata']['echoFunctionId'], data['metadata']['configuration']['ECHO_IMAGE_TAG']),
            (data['metadata']['thumbnailerFunctionId'], data['metadata']['configuration']['THUMBNAILER_IMAGE_TAG'])
        ]
    
    # Insert or replace function images
    cursor.executemany('''
    INSERT OR REPLACE INTO function_images (function_id, image_tag)
    VALUES (?, ?)
    ''', function_images)
    
    conn.commit()
    
    # Print statistics
    print("\nImported function images:")
    cursor.execute("SELECT * FROM function_images")
    for row in cursor.fetchall():
        print(f"  Function {row[0]}: {row[1]}")
        

def add_function_ids_to_cpu_mem_stats(conn):
    """Fill function IDs in cpu_mem_stats table"""
    cursor = conn.cursor()
    cursor.execute('''
    UPDATE cpu_mem_stats
    SET function_id = (
        SELECT su.function_id
        FROM status_updates su
        WHERE su.instance_id = cpu_mem_stats.instance_id
        AND su.function_id IS NOT NULL
        ORDER BY su.timestamp DESC
        LIMIT 1
    )
    WHERE function_id IS NULL
    ''')
    conn.commit()
    

def import_csv_to_sqlite(csv_file='test_results.csv', db_file='metrics.db', json_file='generated_scenarios.json'):
    conn = sqlite3.connect(db_file)
    create_tables(conn)
    
    # First import function images from JSON
    import_function_images(conn, json_file)
    
    cursor = conn.cursor()

    # to collect metrics for each request
    requests = defaultdict(dict)
    
    # collect all metrics by request_id
    with open(csv_file, 'r') as file:
        csv_reader = csv.DictReader(file)
        for row in csv_reader:
            # Skip setup rows
            if '::setup' in row['group']:
                continue
                
            # Skip dropped iterations
            if row['metric_name'] == 'dropped_iterations' or row['metric_name'] == 'iteration_duration' or row['metric_name'] == 'iterations':
                continue
            
            # Extract request identifier
            scenario = row['scenario']
            metadata = row['metadata']
            
            # Skip rows without proper metadata or scenario
            if not scenario or not metadata:
                continue
                
            # The metadata can be used as a request_id
            # Transform metadata string into consistent request_key format
            metadata_dict = {k: v for part in metadata.split('&') for k, v in [part.split('=')]}
            
            # Create unique request key with run_id if available
            run_id = None
            if row['extra_tags']:
                tags = parse_tags(row['extra_tags'])
                if 'run_id' in tags:
                    run_id = tags['run_id']
            
            if run_id:
                request_key = f"run={run_id}&vu={metadata_dict['vu']}&iter={metadata_dict['iter']}"
            else:
                # Single run format
                request_key = f"vu={metadata_dict['vu']}&iter={metadata_dict['iter']}"
            
            # Store common metadata for this request
            requests[request_key]['scenario'] = scenario
            requests[request_key]['service'] = row['service']
            requests[request_key]['timestamp'] = row['timestamp']
            requests[request_key]['request_id'] = request_key
            
            # Parse extra_tags to extract image_tag
            if row['extra_tags']:
                tags = parse_tags(row['extra_tags'])
                if 'image_tag' in tags:
                    requests[request_key]['image_tag'] = tags['image_tag']
                if 'instanceId' in tags:
                    requests[request_key]['instance_id'] = tags['instanceId']
                if 'functionParameters' in tags:
                    requests[request_key]['function_parameters'] = tags['functionParameters']
                    
            # Store proto and subproto info
            requests[request_key]['proto'] = row['proto']
            requests[request_key]['subproto'] = row['subproto']
            requests[request_key]['group_name'] = row['group']
            requests[request_key]['extra_tags'] = row['extra_tags']
            
            # Store callqueuedtimestamp, gotresponsetimestamp, data_sent, data_received
            if row['metric_name'] == 'callqueuedtimestamp':
                requests[request_key]['callqueuedtimestamp'] = row['metric_value']
            elif row['metric_name'] == 'gotresponsetimestamp':
                requests[request_key]['gotresponsetimestamp'] = row['metric_value']
            elif row['metric_name'] == 'data_sent':
                requests[request_key]['data_sent'] = row['metric_value']
            elif row['metric_name'] == 'data_received':
                requests[request_key]['data_received'] = row['metric_value']
            elif row['metric_name'] == 'grpc_req_duration':
                requests[request_key]['grpc_req_duration'] = row['metric_value']
            elif row['metric_name'] == 'leafgotrequesttimestamp':
                requests[request_key]['leafgotrequesttimestamp'] = row['metric_value']
            elif row['metric_name'] == 'leafscheduledcalltimestamp':
                requests[request_key]['leafscheduledcalltimestamp'] = row['metric_value']
            elif row['metric_name'] == 'functionprocessingtime':
                requests[request_key]['functionprocessingtime'] = row['metric_value']
            elif row['metric_name'] == 'timeout':
                requests[request_key]['timeout'] = row['metric_value']
            elif row['metric_name'] == 'error':
                requests[request_key]['error'] = row['metric_value']
    # insert collected requests into the database
    for request_key, data in requests.items():
        # skip requests that have no grpc_req_duration, error, or timeout
        # I don't really understand why k6 logs this in this case. I think its related to the TCP connection issue.
        if (
            data.get('grpc_req_duration') is None and
            data.get('timeout') is None and
            data.get('error') is None
        ):
            continue
        cursor.execute('''
        INSERT INTO metrics (
            timestamp, scenario, service, image_tag, instance_id, request_id,
            grpc_req_duration, callqueuedtimestamp, gotresponsetimestamp, functionprocessingtime,
            leafgotrequesttimestamp, leafscheduledcalltimestamp,
            data_sent, data_received, function_parameters,
            proto, subproto, group_name, extra_tags, timeout, error
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ''', (
            data.get('timestamp'),
            data.get('scenario'),
            data.get('service'),
            data.get('image_tag'),
            data.get('instance_id'),
            data.get('request_id'),
            data.get('grpc_req_duration'),
            data.get('callqueuedtimestamp'),
            data.get('gotresponsetimestamp'),
            data.get('functionprocessingtime'),
            data.get('leafgotrequesttimestamp'),
            data.get('leafscheduledcalltimestamp'),
            data.get('data_sent'),
            data.get('data_received'),
            data.get('function_parameters'),
            data.get('proto'),
            data.get('subproto'),
            data.get('group_name'),
            data.get('extra_tags'),
            data.get('timeout'),
            data.get('error'),
        ))
    
    conn.commit()

    print(f"Imported {len(requests)} requests into the database")
    
    conn.close()

if __name__ == "__main__":
    import argparse
    
    parser = argparse.ArgumentParser(description='Import metrics from CSV to SQLite')
    parser.add_argument('--csv', default='test_results.csv', help='Path to CSV file')
    parser.add_argument('--db', default='metrics.db', help='Path to SQLite database')
    parser.add_argument('--json', default='generated_scenarios.json', help='Path to scenarios JSON file')
    parser.add_argument('--add-function-ids', default=False, help='Add function IDs to cpu_mem_stats table')
    args = parser.parse_args()
    import_csv_to_sqlite(args.csv, args.db, args.json)
    if args.add_function_ids:
        conn = sqlite3.connect(args.db)
        try:
            add_function_ids_to_cpu_mem_stats(conn)
        except sqlite3.OperationalError as e:
            print(f"Error adding function IDs to cpu_mem_stats: {e}")
            print("Did you forget to run the metrics-client? [just metrics-client]")
        conn.close()