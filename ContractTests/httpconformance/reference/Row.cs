// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

using Cratis.Arc.Queries.ModelBound;

namespace HttpConformance;

[ReadModel]
public record Row(int Id, int Score, bool Active, string Label)
{
    static Row[] Rows() => [new(3, 10, true, "row-three"), new(1, 30, false, "row-one"), new(4, 20, true, "row-four"), new(2, 40, false, "row-two")];

    [Path("/api/plain")]
    public static IEnumerable<Row> Plain() => Rows();

    [Path("/api/renderable")]
    public static IQueryable<Row> Renderable() => Rows().AsQueryable();

    [Path("/api/filter")]
    public static IQueryable<Row> Filter(int min = 0, bool active = true, string prefix = "") =>
        Rows().Where(row => row.Score >= min && row.Active == active && row.Label.StartsWith(prefix, StringComparison.Ordinal)).AsQueryable();

    [Path("/api/failing")]
    public static IEnumerable<Row> Failing() => throw new InvalidOperationException("fixture failure");
}
