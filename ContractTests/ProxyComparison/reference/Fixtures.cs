// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

using System.Reactive.Subjects;
using Cratis.Arc.Commands;
using Cratis.Arc.Commands.ModelBound;
using Cratis.Arc.Queries.ModelBound;
using Cratis.Serialization;
using FluentValidation;

namespace ProxyComparison;

public enum Status
{
    Draft = 0,
    Published = 1
}

public record Detail(Guid Id, DateTimeOffset Created);

// The shared HTTP host's derived-type discovery requires an interface registration target.
public interface INotice
{
    string Title { get; }
}

public record Notice(string Title) : INotice;

[DerivedType("1578f20a-cd63-456f-98aa-c97daf05d0fa", typeof(INotice))]
public record UrgentNotice(string Title, int Priority) : Notice(Title);

[Command]
public record Register(Guid Id, string Name, int Quantity)
{
    public void Handle() { }
}

public sealed class RegisterValidator : CommandValidator<Register>
{
    public RegisterValidator()
    {
        RuleFor(command => command.Name).NotEmpty().WithMessage("Name required");
        RuleFor(command => command.Name).MaximumLength(40).WithMessage("Name too long");
        RuleFor(command => command.Quantity).GreaterThanOrEqualTo(1).WithMessage("Quantity must be positive");
    }
}

[ReadModel]
public record Listing(string Name, Detail Detail, Notice Notice, Status Status)
{
    [Cratis.Arc.Authorization.AllowAnonymous]
    [Cratis.Arc.Queries.ModelBound.Path("/api/proxy-comparison/all")]
    public static IQueryable<Listing> All(Guid id) => Rows(id).AsQueryable();

    [Cratis.Arc.Authorization.AllowAnonymous]
    [Cratis.Arc.Queries.ModelBound.Path("/api/proxy-comparison/observe")]
    public static ISubject<Listing[]> Observe(Guid id) => new BehaviorSubject<Listing[]>(Rows(id));

    static Listing[] Rows(Guid id) =>
    [
        new("charlie", new(id, DateTimeOffset.Parse("2026-01-03T03:04:05Z")), new Notice("third"), Status.Draft),
        new("alpha", new(id, DateTimeOffset.Parse("2026-01-01T03:04:05Z")), new Notice("first"), Status.Published),
        new("bravo", new(id, DateTimeOffset.Parse("2026-01-02T03:04:05Z")), new Notice("second"), Status.Draft)
    ];
}
